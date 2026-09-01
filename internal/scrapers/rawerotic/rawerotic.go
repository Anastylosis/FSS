// Package rawerotic scrapes rawerotic.com, which runs the LetsDoeIt group's
// "superbe" tour (Vue components, `p.cdnc.letsdoeit.com` assets).
//
// Listing card at `/videos.en.html?order=-recent&page={N}`:
//
//	<div class="global-multi-card global-multi-card-films …">
//	  <video-preview src="https://p.cdnc.letsdoeit.com/movie/teaser/…/teaser-mobile-lemonade-….mp4">
//	    <a class="-gmc-fake" href="https://rawerotic.com/watch/76142/lemonade.en.html"></a>
//	    <div class="-gmc-thumb lazy" data-bg="https://p.cdnc.letsdoeit.com/photo/crop/512x288/…jpg"></div>
//	    <div class="-gmc-text-overlay">
//	      <div class="-gmcto-small">Baby Nicols</div>
//	      <div class="-gmcto-large">Lemonade</div>
//	    </div>…
//
// The card names the scene and its cast; the detail page carries a schema.org
// VideoObject **as microdata**, not JSON-LD — `<meta itemprop="duration"
// content="T12M17S">` and friends — which is where the runtime, publication
// date and description come from.
package rawerotic

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one site on this tour.
type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	// SuffixName is the site label the tour appends to every description
	// ("RawErotic"), which is trimmed back off. It differs from StudioName,
	// which is the studio's name as StashDB spells it.
	SuffixName string
}

var sites = []SiteConfig{
	{SiteID: "rawerotic", Domain: "rawerotic.com", StudioName: "rawerotic", SuffixName: "RawErotic"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return New(cfg)
		}
	}
	return nil
}

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		base:    "https://" + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{s.cfg.Domain, s.cfg.Domain + "/videos.en.html"}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: scraping full catalogue", s.cfg.SiteID)
	items, ok := s.collectListing(ctx, opts, out)
	if !ok || ctx.Err() != nil {
		return
	}
	s.fetchDetails(ctx, studioURL, items, opts, out)
}

func (s *Scraper) collectListing(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) (items []listItem, ok bool) {
	seen := make(map[string]bool)

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return items, false
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return items, false
			}
		}

		// `order=-recent` is what makes the walk newest-first, which is what
		// the KnownIDs early-stop needs.
		pageURL := fmt.Sprintf("%s/videos.en.html?order=-recent&page=%d", s.base, page)
		scraper.Debugf(1, "%s: fetching listing page %d (%s)", s.cfg.SiteID, page, pageURL)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return items, false
		}

		parsed := parseListing(body)
		// Past the last page the listing renders no cards, which is the end.
		if len(parsed) == 0 {
			return items, true
		}

		fresh := 0
		for _, item := range parsed {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			if opts.KnownIDs[item.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.SiteID, item.id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return items, true
			}
			fresh++
			items = append(items, item)
		}
		if fresh == 0 {
			return items, true
		}
	}
}

func (s *Scraper) fetchDetails(ctx context.Context, studioURL string, items []listItem, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(items) == 0 {
		return
	}
	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)

	work := make(chan listItem, workers)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer close(work)

	now := time.Now().UTC()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				body, err := s.fetchPage(ctx, item.url)
				if err != nil {
					// The card already names the scene and its cast; a detail
					// page that will not load costs the runtime, date and
					// description.
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.url, err)):
					case <-ctx.Done():
						return
					}
				} else {
					enrichFromDetail(body, &item)
				}
				select {
				case out <- scraper.Scene(s.toScene(item, studioURL, now)):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, item := range items {
		select {
		case work <- item:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- parsing ----

type listItem struct {
	id          string
	url         string
	title       string
	thumbnail   string
	preview     string
	performers  []string
	date        string
	duration    int
	description string
}

var (
	cardStartRe = regexp.MustCompile(`<div class="global-multi-card global-multi-card-films`)
	cardURLRe   = regexp.MustCompile(`<a class="-gmc-fake" href="([^"]*/watch/(\d+)/[^"]*)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<div class="-gmcto-large">(.*?)</div>`)
	cardModelRe = regexp.MustCompile(`(?s)<div class="-gmcto-small">(.*?)</div>`)
	cardThumbRe = regexp.MustCompile(`data-bg="([^"]+)"`)
	cardTeaseRe = regexp.MustCompile(`<video-preview[^>]+src="([^"]+\.mp4)"`)

	// The detail page carries a schema.org VideoObject as microdata rather
	// than JSON-LD, so parseutil's JSON-LD helpers do not see it.
	itemPropRe = regexp.MustCompile(`<meta itemprop="([a-zA-Z]+)" content="([^"]*)"`)
	tagStripRe = regexp.MustCompile(`<[^>]*>`)
)

func parseListing(body []byte) []listItem {
	locs := cardStartRe.FindAllIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		m := cardURLRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		item := listItem{url: html.UnescapeString(string(m[1])), id: string(m[2])}
		if t := cardTitleRe.FindSubmatch(block); t != nil {
			item.title = cleanText(string(t[1]))
		}
		if t := cardModelRe.FindSubmatch(block); t != nil {
			if n := cleanText(string(t[1])); n != "" {
				item.performers = []string{n}
			}
		}
		if t := cardThumbRe.FindSubmatch(block); t != nil {
			item.thumbnail = html.UnescapeString(string(t[1]))
		}
		if t := cardTeaseRe.FindSubmatch(block); t != nil {
			item.preview = html.UnescapeString(string(t[1]))
		}
		if item.title == "" {
			continue
		}
		items = append(items, item)
	}
	return items
}

func enrichFromDetail(body []byte, item *listItem) {
	for _, m := range itemPropRe.FindAllSubmatch(body, -1) {
		value := cleanText(string(m[2]))
		switch string(m[1]) {
		case "duration":
			if d := parseutil.ParseDurationISO(normalizeISODuration(value)); d > 0 {
				item.duration = d
			}
		case "uploadDate":
			item.date = value
		case "description":
			if value != "" {
				item.description = value
			}
		case "thumbnailUrl":
			if value != "" {
				item.thumbnail = value
			}
		}
	}
}

// normalizeISODuration repairs the tour's malformed duration. It writes
// "T12M17S" where ISO 8601 requires a leading "P", and parseutil rejects it
// without one.
func normalizeISODuration(v string) string {
	if strings.HasPrefix(v, "T") {
		return "P" + v
	}
	return v
}

func cleanText(s string) string {
	s = html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// trimSiteSuffix drops the " - RawErotic - RawErotic" the tour appends to
// every description. The site name is repeated, so the trim loops.
func trimSiteSuffix(desc, siteName string) string {
	suffix := " - " + siteName
	for {
		trimmed := strings.TrimSuffix(desc, suffix)
		if trimmed == desc {
			return strings.TrimSpace(desc)
		}
		desc = trimmed
	}
}

func (s *Scraper) toScene(item listItem, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          item.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         item.url,
		Description: trimSiteSuffix(item.description, s.cfg.SuffixName),
		Thumbnail:   item.thumbnail,
		Preview:     item.preview,
		Performers:  item.performers,
		Studio:      s.cfg.StudioName,
		Duration:    item.duration,
		ScrapedAt:   now,
	}
	if d, err := parseutil.TryParseDate(item.date, time.RFC3339, "2006-01-02"); err == nil {
		sc.Date = d.UTC()
	}
	return sc
}
