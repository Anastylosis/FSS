package mydirtyhobby

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	defaultSiteBase    = "https://www.mydirtyhobby.com"
	defaultContentBase = "https://www.mydirtyhobby.com"
	// defaultPageSize is the site's own documented maximum: asking for 500
	// returns "The page size must not be greater than 300." It is set there
	// deliberately, because the WAF challenges a client after roughly seventeen
	// requests in a window — what decides whether a profile can be walked at
	// all is the number of requests, not their spacing. At 20 per page this
	// scraper worked until a catalogue passed ~340 scenes and then began
	// stopping mid-walk; at 300 a 1,096-scene profile is four requests. Fewer,
	// larger requests are also the gentler thing to ask of the site.
	defaultPageSize = 300
)

// Scraper implements scraper.StudioScraper for MyDirtyHobby.
type Scraper struct {
	client      *http.Client
	siteBase    string
	contentBase string
	pageSize    int
}

func New() *Scraper {
	return &Scraper{
		client:      httpx.NewClient(30 * time.Second),
		siteBase:    defaultSiteBase,
		contentBase: defaultContentBase,
		pageSize:    defaultPageSize,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

// ---- StudioScraper interface ----

func (s *Scraper) ID() string { return "mydirtyhobby" }

func (s *Scraper) Patterns() []string {
	return []string{
		"mydirtyhobby.com/profil/{userId}-{username}",
		"mydirtyhobby.com/profil/{userId}-{username}/videos",
	}
}

// matchRe gates MatchesURL to only mydirtyhobby.com URLs.
var matchRe = regexp.MustCompile(`^https?://(?:www\.)?mydirtyhobby\.com/profil/\d+-`)

// profileRe extracts the user ID and nick from any URL containing /profil/{id}-{nick}.
var profileRe = regexp.MustCompile(`/profil/(\d+)-([^/?]+)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	uid, nick, err := profileParams(studioURL)
	if err != nil {
		return nil, err
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, uid, nick, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, uid int, nick string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	// The profile video API exposes no sort parameter and returns items roughly
	// oldest-first (live-verified against Dirty-Tina: the first page runs
	// 2023-07 → 2023-10 ascending). A KnownIDs early-stop would therefore halt
	// on the first stored scene and never reach anything newer, so the hint is
	// dropped — there is only one listing mode here.
	opts.KnownIDs = nil

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, "mydirtyhobby", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		items, total, totalPages, err := s.fetchPage(ctx, uid, page, opts.Cookie)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = toScene(studioURL, s.siteBase, uid, nick, item, now)
		}
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   page >= totalPages,
		}, nil
	})
}

// ---- API call ----

type listRequest struct {
	Page         int    `json:"page"`
	PageSize     int    `json:"pageSize"`
	UserID       int    `json:"user_id"`
	UserLanguage string `json:"user_language"`
}

type listResponse struct {
	Items      []mdhItem `json:"items"`
	Total      int       `json:"total"`
	Page       int       `json:"page"`
	TotalPages int       `json:"totalPages"`
}

type mdhItem struct {
	UID                 int     `json:"u_id"`
	UVID                int     `json:"uv_id"`
	Nick                string  `json:"nick"`
	Title               string  `json:"title"`
	Description         string  `json:"description"`
	Thumbnail           string  `json:"thumbnail"`
	Price               string  `json:"price"`
	HasDiscount         bool    `json:"hasDiscount"`
	ReducedPercent      *int    `json:"reducedPercent"`
	VotesAverage        float64 `json:"votesAverage"`
	VotesCount          int     `json:"votesCount"`
	Duration            string  `json:"duration"`
	LatestPictureChange string  `json:"latestPictureChange"`
	Language            string  `json:"language"`
}

func (s *Scraper) fetchPage(ctx context.Context, uid, page int, cookie string) ([]mdhItem, int, int, error) {
	body, err := json.Marshal(listRequest{
		Page:         page,
		PageSize:     s.pageSize,
		UserID:       uid,
		UserLanguage: "en",
	})
	if err != nil {
		return nil, 0, 0, err
	}

	u := s.contentBase + "/content/api/v2/videos"
	var lr listResponse
	// XHRHeaders, not BrowserHeaders: the WAF challenges a navigation-shaped
	// request to this JSON endpoint. DoJSON, not Do: the challenge arrives as
	// HTTP 200, so it reads as a parse failure and would abort the whole walk.
	// See docs/scrapers.md.
	err = httpx.DoJSON(ctx, s.client, httpx.Request{
		URL:  u,
		Body: body,
		Headers: func() map[string]string {
			h := httpx.XHRHeaders(httpx.UserAgentFirefox, s.contentBase)
			h["Cookie"] = cookieHeader(cookie)
			return h
		}(),
	}, &lr)
	if err != nil {
		return nil, 0, 0, err
	}
	return lr.Items, lr.Total, lr.TotalPages, nil
}

// cookieHeader combines the age gate — which the site sets from a button, not a
// login — with whatever the operator supplied via --site-cookie. The WAF here
// answers an unrecognised client with a JavaScript challenge rather than a
// status code (see docs/scrapers.md), and the cookie it hands a browser that
// passes is the only thing that lifts it. fss neither solves nor refreshes that
// challenge: the operator passes it in their own browser and pastes the cookie.
func cookieHeader(operator string) string {
	const ageGate = "AGEGATEPASSED=1"
	operator = strings.TrimSpace(strings.Trim(strings.TrimSpace(operator), ";"))
	if operator == "" {
		return ageGate
	}
	return ageGate + "; " + operator
}

// ---- mapping ----

func toScene(studioURL, siteBase string, uid int, nick string, item mdhItem, now time.Time) models.Scene {
	regularCents, priceErr := strconv.ParseFloat(item.Price, 64)
	regular := regularCents / 100

	var discounted float64
	var discountPct int
	if item.HasDiscount && item.ReducedPercent != nil {
		discountPct = *item.ReducedPercent
		discounted = regular * float64(100-discountPct) / 100
	}

	scene := models.Scene{
		ID:          strconv.Itoa(item.UVID),
		SiteID:      "mydirtyhobby",
		StudioURL:   studioURL,
		Title:       item.Title,
		URL:         fmt.Sprintf("%s/profil/%d-%s/videos/%d", siteBase, uid, url.PathEscape(nick), item.UVID),
		Date:        parseDate(item.LatestPictureChange),
		Description: item.Description,
		Thumbnail:   item.Thumbnail,
		Studio:      item.Nick,
		Duration:    parseutil.ParseDurationColon(item.Duration),
		Likes:       item.VotesCount,
		ScrapedAt:   now,
	}

	if priceErr == nil {
		scene.AddPrice(models.PriceSnapshot{
			Date:            now,
			Regular:         regular,
			Discounted:      discounted,
			IsFree:          regular == 0,
			IsOnSale:        item.HasDiscount,
			DiscountPercent: discountPct,
		})
	}

	return scene
}

// ---- helpers ----

func profileParams(studioURL string) (int, string, error) {
	m := profileRe.FindStringSubmatch(studioURL)
	if m == nil {
		return 0, "", fmt.Errorf("cannot extract profile ID from %q", studioURL)
	}
	uid, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", fmt.Errorf("invalid profile ID %q: %w", m[1], err)
	}
	// Strip any trailing path segment from slug (e.g., "/videos").
	nick := strings.SplitN(m[2], "/", 2)[0]
	return uid, nick, nil
}

// parseDate parses ISO 8601 timestamps with timezone offset.
func parseDate(s string) time.Time {
	t, _ := parseutil.TryParseDate(s, time.RFC3339, "2006-01-02T15:04:05-07:00")
	return t.UTC()
}
