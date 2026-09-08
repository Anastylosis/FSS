// Package mfcshare scrapes model catalogues on share.myfreecams.com.
package mfcshare

import (
	"context"
	"errors"
	"fmt"
	"html"
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
	host       = "share.myfreecams.com"
	defaultURL = "https://share.myfreecams.com"
	// The album grid renders 16 cards per request and ignores a larger limit.
	pageSize = 16
	// RecommendedDelay is the floor an operator should keep. The site budgets
	// requests per address over a short window — about six get through before
	// it answers 429, and httpx's 0s/2s/4s ladder is not long enough to clear
	// it — so 56 listing requests for a 900-album catalogue is close to what
	// can be asked for in one go.
	RecommendedDelay = 2 * time.Second
)

// SiteConfig describes one model's catalogue.
type SiteConfig struct {
	SiteID   string
	Studio   string
	Username string
	// Aliases are former usernames the site still redirects from. They are
	// matched so an operator's old bookmark — and the URL StashDB records —
	// still routes here.
	Aliases []string
}

var sites = []SiteConfig{
	{SiteID: "kerriking", Studio: "Kerri King", Username: "KerriKing"},
	{
		SiteID: "exquisitegoddess", Studio: "ExquisiteGoddess",
		Username: "GoddessOfPleasure",
		Aliases:  []string{"ErectionRX"},
	},
}

type Scraper struct {
	Client *http.Client
	cfg    SiteConfig
	base   string
}

// New builds a scraper for one model.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		Client: httpx.NewClient(45 * time.Second),
		cfg:    cfg,
		base:   defaultURL,
	}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	pats := []string{host + "/" + s.cfg.Username, host + "/" + s.cfg.Username + "/albums"}
	for _, a := range s.cfg.Aliases {
		pats = append(pats, host+"/"+a)
	}
	return pats
}

func (s *Scraper) matchRe() *regexp.Regexp {
	names := append([]string{s.cfg.Username}, s.cfg.Aliases...)
	for i, n := range names {
		names[i] = regexp.QuoteMeta(n)
	}
	return regexp.MustCompile(`(?i)^https?://` + regexp.QuoteMeta(host) +
		`/(?:` + strings.Join(names, "|") + `)(?:/.*)?$`)
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe().MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- listing ----

var (
	// The rendered grid is a placeholder skeleton; `layout=false` returns the
	// real cards, which is the same partial the page's infinite scroll asks
	// for.
	cardSplitRe = regexp.MustCompile(`<div id="album-`)
	slugRe      = regexp.MustCompile(`data-slug="([^"]+)"`)
	typeRe      = regexp.MustCompile(`(?is)<album-box-piece[^>]*data-type="([^"]+)"`)
	titleRe     = regexp.MustCompile(`(?is)<span class="title">(.*?)</span>`)
	thumbRe     = regexp.MustCompile(`(?is)<img[^>]*class="default-thumbnail"[^>]*\ssrc="([^"]+)"`)
	durationRe  = regexp.MustCompile(`(?is)<i class="icon-play"></i>\s*(\d{1,2}:\d{2}(?::\d{2})?)`)
)

type albumCard struct {
	slug      string
	title     string
	thumbnail string
	duration  int
}

// parseAlbums reads one grid partial, keeping only video albums. MFC Share
// hosts photo sets in the same grid and under the same URL shape; storing one
// as a scene would file a gallery as a video.
func parseAlbums(body []byte) []albumCard {
	var out []albumCard
	seen := map[string]bool{}
	for _, block := range cardSplitRe.Split(string(body), -1)[1:] {
		t := typeRe.FindStringSubmatch(block)
		if t == nil || !strings.EqualFold(t[1], "Video") {
			continue
		}
		m := slugRe.FindStringSubmatch(block)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		c := albumCard{slug: m[1]}
		if x := titleRe.FindStringSubmatch(block); x != nil {
			c.title = cleanText(x[1])
		}
		if x := thumbRe.FindStringSubmatch(block); x != nil {
			c.thumbnail = html.UnescapeString(x[1])
		}
		if x := durationRe.FindStringSubmatch(block); x != nil {
			c.duration = parseutil.ParseDurationColon(x[1])
		}
		out = append(out, c)
	}
	return out
}

// ---- run ----

func (s *Scraper) listingURL(offset int) string {
	q := url.Values{}
	q.Set("layout", "false")
	q.Set("offset", strconv.Itoa(offset))
	return fmt.Sprintf("%s/%s/albums?%s", s.base, s.cfg.Username, q.Encode())
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.Debugf(1, "%s: scraping %s", s.cfg.SiteID, s.cfg.Username)

	scraper.WarnDelayBelow(s.cfg.SiteID, opts.Delay, RecommendedDelay)

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		offset := (page - 1) * pageSize
		u := s.listingURL(offset)
		body, err := s.get(ctx, u)
		if err != nil {
			// An offset past the last album answers 404. That is the end of
			// the catalogue, not a failure, and reporting it would mark every
			// full run incomplete.
			if page > 1 && isNotFound(err) {
				scraper.Debugf(1, "%s: offset %d past the end (404), stopping", s.cfg.SiteID, offset)
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}

		cards := parseAlbums(body)
		if len(cards) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(u, errors.New("no video albums on the first page"))
			}
			return scraper.PageResult{Done: true}, nil
		}

		scenes := make([]models.Scene, 0, len(cards))
		for _, c := range cards {
			scenes = append(scenes, s.baseScene(c, studioURL))
		}
		return scraper.PageResult{
			Scenes: scenes,
			// A page of nothing but photo albums is not the end of the
			// catalogue, so an empty scene slice must not stop the walk.
			Continue: true,
		}, nil
	})
}

func isNotFound(err error) bool {
	var se *httpx.StatusError
	return errors.As(err, &se) && se.StatusCode == http.StatusNotFound
}

// baseScene builds a scene from its grid card.
//
// **There is no detail fetch, and so no release date.** The date is published
// only on the per-album page, and the site budgets requests per address over a
// short window — roughly six before it answers 429, which httpx's retry ladder
// is too short to clear. Fetching 900 album pages for one field each would take
// hours and hammer a host that has said no; asking for fewer requests is the
// right answer to a bot check that counts them, so the walk stops at the grid.
//
// Albums are priced in MFC tokens. Those are deliberately NOT recorded:
// PriceSnapshot carries a bare amount with no currency, so a 300-token clip
// would sort against a $9.99 one in `fss compare` and in Scene.LowestPrice as
// though the two were the same unit.
func (s *Scraper) baseScene(c albumCard, studioURL string) models.Scene {
	title := c.title
	if title == "" {
		title = c.slug
	}
	return models.Scene{
		ID:         c.slug,
		SiteID:     s.cfg.SiteID,
		StudioURL:  studioURL,
		Studio:     s.cfg.Studio,
		Title:      title,
		URL:        s.base + "/a/" + c.slug,
		Thumbnail:  c.thumbnail,
		Duration:   c.duration,
		Performers: []string{s.cfg.Studio},
		ScrapedAt:  time.Now().UTC(),
	}
}

var (
	tagStripRe = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe       = regexp.MustCompile(`\s+`)
)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(html.UnescapeString(s))
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}
