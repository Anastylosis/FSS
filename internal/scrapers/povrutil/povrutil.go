package povrutil

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	ID       string
	Studio   string
	SiteBase string
	Patterns []string
	MatchRe  *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		Client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string               { return s.cfg.ID }
func (s *Scraper) Patterns() []string       { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool { return s.cfg.MatchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

// exportVideo is one entry from /export/videos.json.
type exportVideo struct {
	URL    string   `json:"url"`
	Length int      `json:"length"`
	Title  string   `json:"title"`
	Tags   []string `json:"tags"`
	Thumb  string   `json:"thumb"`
	Actors []string `json:"actors"`
}

var sceneIDRe = regexp.MustCompile(`-(\d+)$`)

func extractID(rawURL string) string {
	rawURL = strings.TrimSuffix(rawURL, "/")
	if m := sceneIDRe.FindStringSubmatch(rawURL); m != nil {
		return m[1]
	}
	return ""
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	// The export is enrichment only — tags, duration, thumbnail and a fallback
	// cast, all keyed off the listing walk that follows. Aborting when it
	// fails threw away the whole catalogue on all four sites for a file that
	// costs four fields, so the failure is reported and the walk continues
	// with an empty lookup.
	scraper.Debugf(1, "%s: fetching video export", s.cfg.ID)
	export, err := s.fetchExport(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("export: %w", err)):
		case <-ctx.Done():
			return
		}
	}

	lookup := make(map[string]exportVideo, len(export))
	for _, v := range export {
		path := urlPath(v.URL)
		if path != "" {
			lookup[path] = v
		}
	}

	if len(export) > 0 {
		scraper.Debugf(1, "%s: %d total scenes from export", s.cfg.ID, len(export))
		select {
		case out <- scraper.Progress(len(export)):
		case <-ctx.Done():
			return
		}
	}

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		cards, hasNext, err := s.fetchListingPage(ctx, page)
		if err != nil {
			return scraper.PageResult{}, err
		}

		var scenes []models.Scene
		for _, c := range cards {
			id := extractID(c.path)
			if id == "" {
				continue
			}
			scenes = append(scenes, s.buildScene(c, lookup[c.path], id, now))
		}
		// Stop on the site's own marker rather than by overshooting into the
		// 404. Only when the page actually held cards: no cards and no marker
		// is a parse failure, and Paginate already ends on an empty page, so
		// leaving Done unset there keeps that case reported as it was.
		return scraper.PageResult{Scenes: scenes, Done: len(cards) > 0 && !hasNext}, nil
	})
}

func (s *Scraper) buildScene(c listingCard, ev exportVideo, id string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:         id,
		SiteID:     s.cfg.ID,
		StudioURL:  s.cfg.SiteBase,
		Title:      c.title,
		URL:        s.cfg.SiteBase + c.path,
		Date:       c.date,
		Performers: c.performers,
		Studio:     s.cfg.Studio,
		ScrapedAt:  now,
	}

	if ev.URL != "" {
		sc.Tags = ev.Tags
		sc.Duration = ev.Length
		sc.Thumbnail = ev.Thumb
		if len(sc.Performers) == 0 {
			sc.Performers = ev.Actors
		}
	}

	return sc
}

// ---- export fetch ----

func (s *Scraper) fetchExport(ctx context.Context) ([]exportVideo, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL: s.cfg.SiteBase + "/export/videos.json",
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Accept"] = "application/json"
			return h
		}(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var videos []exportVideo
	if err := httpx.DecodeJSON(resp.Body, &videos); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return videos, nil
}

// ---- listing page parsing ----

type listingCard struct {
	path       string
	title      string
	performers []string
	date       time.Time
}

var (
	cardRe      = regexp.MustCompile(`(?s)cards-list__item card\s*">(.*?)</div>\s*</div>\s*</div>\s*</div>`)
	cardLinkRe  = regexp.MustCompile(`<a href="(/[^"]+)" class="card__video`)
	cardTitleRe = regexp.MustCompile(`card__h">([^<]+)`)
	cardPerfsRe = regexp.MustCompile(`card__links">(.*?)</div>`)
	perfNameRe  = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
	cardDateRe  = regexp.MustCompile(`card__date">.*?</svg>\s*(\d{1,2}\s\w+,\s\d{4})`)
)

func parseListingCards(body []byte) []listingCard {
	matches := cardRe.FindAllSubmatch(body, -1)
	cards := make([]listingCard, 0, len(matches))
	for _, m := range matches {
		block := m[1]
		c := listingCard{}

		if lm := cardLinkRe.FindSubmatch(block); lm != nil {
			c.path = string(lm[1])
		}
		if tm := cardTitleRe.FindSubmatch(block); tm != nil {
			c.title = strings.TrimSpace(string(tm[1]))
		}
		if pm := cardPerfsRe.FindSubmatch(block); pm != nil {
			for _, nm := range perfNameRe.FindAllSubmatch(pm[1], -1) {
				name := strings.TrimSpace(string(nm[1]))
				if name != "" {
					c.performers = append(c.performers, name)
				}
			}
		}
		if dm := cardDateRe.FindSubmatch(block); dm != nil {
			if t, err := time.Parse("2 January, 2006", string(dm[1])); err == nil {
				c.date = t.UTC()
			}
		}

		if c.path != "" {
			cards = append(cards, c)
		}
	}
	return cards
}

// hasNextRe marks that another listing page exists. It is the site's own
// end-of-list signal and is what keeps the walk off the past-end page, which
// answers 404 — read as an error that made every full run report a failure and
// demoted the traversal to non-authoritative. Live-checked on milfvr: present
// on page 29, absent on the last page 30, and page 31 is the 404.
var hasNextRe = regexp.MustCompile(`rel="next"`)

func (s *Scraper) fetchListingPage(ctx context.Context, page int) ([]listingCard, bool, error) {
	u := fmt.Sprintf("%s/?o=d&p=%d", s.cfg.SiteBase, page)
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL: u,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Accept"] = "text/html"
			return h
		}(),
	})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, false, err
	}
	return parseListingCards(body), hasNextRe.Match(body), nil
}

func urlPath(rawURL string) string {
	i := strings.Index(rawURL, "://")
	if i < 0 {
		return ""
	}
	rest := rawURL[i+3:]
	j := strings.Index(rest, "/")
	if j < 0 {
		return ""
	}
	return rest[j:]
}
