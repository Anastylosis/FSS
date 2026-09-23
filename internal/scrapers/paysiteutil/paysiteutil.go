// Package paysiteutil scrapes sites running the Paysite.com Next.js template:
// a server-rendered app whose listing ships as a `<script id="__NEXT_DATA__">`
// JSON blob under `props.pageProps.contents` — total, total_pages and a `data`
// array whose items already carry title, slug, publish_date, seconds_duration,
// thumb, tags, models and description. There is no public detail page: the
// card's own `link` redirects through the site's NATS join domain, so every
// field comes from the listing payload.
//
// The listing is date-sorted (`order_by=publish_date, sort_by=desc`), so the
// KnownIDs early-stop applies. Model pages (`/models/{slug}`) carry their whole
// filmography in one payload under `pageProps.model.contents`.
//
// Registered by the table-driven packages `kbproductions` and `paysitenext`.
package paysiteutil

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
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one site served by this template.
type SiteConfig struct {
	SiteID     string // stable lowercase id, e.g. "rickysroom"
	Domain     string // bare domain, e.g. "rickysroom.com"
	StudioName string // fallback studio name; the payload's own `site` wins
	// ListPath is the listing path segment, without slashes. Empty means
	// "videos"; a few sites (Yes Girlz) serve the same template at "scenes".
	ListPath string
	// Aliases are extra domains that serve the same site — an apex that
	// redirects to a tour subdomain, say. They widen MatchesURL only: Domain
	// stays what gets fetched and what `fss list-scrapers` shows.
	Aliases []string
	// ModelPattern overrides the model-page pattern shown by `fss
	// list-scrapers`. Empty means "{domain}/models/{slug}"; some sites number
	// their model URLs "{domain}/models/{id}-{slug}". Display only — MatchesURL
	// accepts any path on the domain either way.
	ModelPattern string
}

// listPath returns the configured listing segment, defaulting to "videos".
func (c SiteConfig) listPath() string {
	if c.ListPath == "" {
		return "videos"
	}
	return c.ListPath
}

// New builds a registered-ready scraper for one site.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		matchRe: domainRe(cfg),
	}
}

// baseFor picks the origin to fetch from: the operator's own host, which may
// carry http:// or www. and is what points offline tests at an httptest server.
// A host named in Aliases is the exception — it only redirects to the real one
// and serves nothing under the listing path itself.
func (s *Scraper) baseFor(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil || u.Host == "" {
		return "https://" + s.cfg.Domain
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	for _, alias := range s.cfg.Aliases {
		if host == strings.ToLower(alias) {
			return "https://" + s.cfg.Domain
		}
	}
	return u.Scheme + "://" + u.Host
}

// domainRe matches the site's own domain and any alias it answers to.
func domainRe(cfg SiteConfig) *regexp.Regexp {
	quoted := make([]string, 0, len(cfg.Aliases)+1)
	for _, d := range append([]string{cfg.Domain}, cfg.Aliases...) {
		if d != "" {
			quoted = append(quoted, regexp.QuoteMeta(d))
		}
	}
	return regexp.MustCompile(`^https?://(?:www\.)?(?:` + strings.Join(quoted, "|") + `)(?:/|$)`)
}

// Scraper implements scraper.StudioScraper for one Paysite.com Next.js site.
type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	model := s.cfg.ModelPattern
	if model == "" {
		model = s.cfg.Domain + "/models/{slug}"
	}
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/" + s.cfg.listPath(),
		model,
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// --- Next.js data types ---

type nextData struct {
	Props struct {
		PageProps struct {
			Contents contentResponse `json:"contents"`
			Model    *modelData      `json:"model"`
		} `json:"pageProps"`
	} `json:"props"`
}

type contentResponse struct {
	Total      int           `json:"total"`
	TotalPages int           `json:"total_pages"`
	Data       []contentItem `json:"data"`
}

type contentItem struct {
	ID              int    `json:"id"`
	Title           string `json:"title"`
	Slug            string `json:"slug"`
	PublishDate     string `json:"publish_date"`
	SecondsDuration int    `json:"seconds_duration"`
	// VideosDuration is the same runtime as a decimal-seconds string. Some
	// sites on this template emit only this one (Phoenixxx: "1614.46").
	VideosDuration string `json:"videos_duration"`
	Thumb          string `json:"thumb"`
	// Thumbnail is the same image under the other spelling the template uses.
	Thumbnail    string      `json:"thumbnail"`
	Models       []string    `json:"models"`
	ModelsSlugs  []modelSlug `json:"models_slugs"`
	Tags         []string    `json:"tags"`
	Description  string      `json:"description"`
	ContentPrice float64     `json:"content_price"`
	Site         string      `json:"site"`
}

// duration returns the runtime in whole seconds from whichever of the two
// fields the site populates.
func (c contentItem) duration() int {
	if c.SecondsDuration > 0 {
		return c.SecondsDuration
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(c.VideosDuration), 64)
	if err != nil || secs <= 0 {
		return 0
	}
	return int(secs)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// absoluteURL gives a protocol-relative CDN path a scheme; the template emits
// its image URLs as "//host/path" on several sites.
func absoluteURL(u string) string {
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

type modelSlug struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type modelData struct {
	Contents contentResponse `json:"contents"`
}

var (
	nextRe  = regexp.MustCompile(`(?s)<script\s+id="__NEXT_DATA__"\s+type="application/json">(.*?)</script>`)
	modelRe = regexp.MustCompile(`/models/([\w-]+)`)
)

// --- runner ---

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	base := s.baseFor(studioURL)

	if modelRe.MatchString(studioURL) {
		scraper.Debugf(1, "%s: scraping model page", s.cfg.SiteID)
		s.scrapeModelPage(ctx, studioURL, opts, out, now)
		return
	}

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/%s?page=%d", base, s.cfg.listPath(), page)
		items, total, totalPages, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = s.toScene(item, studioURL, now)
		}
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   len(items) == 0 || (totalPages > 0 && page >= totalPages),
		}, nil
	})
}

func (s *Scraper) scrapeModelPage(ctx context.Context, studioURL string, _ scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	body, err := s.fetchPage(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("model page: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	nd, err := parseNextData(body)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("model page: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	var items []contentItem
	if nd.Props.PageProps.Model != nil {
		items = nd.Props.PageProps.Model.Contents.Data
	}
	if len(items) == 0 {
		items = nd.Props.PageProps.Contents.Data
	}

	scraper.Debugf(1, "%s: model page has %d scenes", s.cfg.SiteID, len(items))
	if len(items) > 0 {
		select {
		case out <- scraper.Progress(len(items)):
		case <-ctx.Done():
			return
		}
	}
	for _, item := range items {
		select {
		case out <- scraper.Scene(s.toScene(item, studioURL, now)):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]contentItem, int, int, error) {
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return nil, 0, 0, err
	}
	nd, err := parseNextData(body)
	if err != nil {
		return nil, 0, 0, err
	}
	c := nd.Props.PageProps.Contents
	return c.Data, c.Total, c.TotalPages, nil
}

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func parseNextData(body []byte) (nextData, error) {
	m := nextRe.FindSubmatch(body)
	if m == nil {
		return nextData{}, fmt.Errorf("__NEXT_DATA__ not found")
	}
	var nd nextData
	if err := json.Unmarshal(m[1], &nd); err != nil {
		return nextData{}, fmt.Errorf("parsing __NEXT_DATA__: %w", err)
	}
	return nd, nil
}

func (s *Scraper) toScene(item contentItem, studioURL string, now time.Time) models.Scene {
	var date time.Time
	if item.PublishDate != "" {
		date, _ = time.Parse("2006/01/02 15:04:05", item.PublishDate)
		date = date.UTC()
	}

	var performers []string
	for _, ms := range item.ModelsSlugs {
		if ms.Name != "" {
			performers = append(performers, ms.Name)
		}
	}
	if len(performers) == 0 {
		performers = item.Models
	}

	studio := s.cfg.StudioName
	if item.Site != "" {
		studio = item.Site
	}

	scene := models.Scene{
		ID:          strconv.Itoa(item.ID),
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Title:       item.Title,
		URL:         fmt.Sprintf("https://%s/%s/%s", s.cfg.Domain, s.cfg.listPath(), item.Slug),
		Thumbnail:   absoluteURL(firstNonEmpty(item.Thumb, item.Thumbnail)),
		Duration:    item.duration(),
		Date:        date,
		Description: item.Description,
		Tags:        item.Tags,
		Performers:  performers,
		Studio:      studio,
		ScrapedAt:   now,
	}
	if item.ContentPrice > 0 {
		scene.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: item.ContentPrice,
		})
	}
	return scene
}
