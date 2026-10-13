// Package zexyvr scrapes zexyvr.com, a custom Laravel VR tour. See
// docs/scrapers.md.
package zexyvr

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "zexyvr"
	studioName = "ZexyVR"
	// detailWorkers caps the per-page detail pool; the cards carry everything
	// except the synopsis and tags.
	detailWorkers = 4
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?zexyvr\.com(?:[/?#]|$)`)
	// A tag page is one /videos/ segment; a scene is two (slug, then id).
	modelPathRe = regexp.MustCompile(`^/models/[^/]+/\d+/?$`)
	tagPathRe   = regexp.MustCompile(`^/videos/[^/]+/?$`)

	cardSplitRe  = regexp.MustCompile(`<div class="card border-0`)
	cardLinkRe   = regexp.MustCompile(`<a href="([^"]+)" class="card-link">(.*?)</a>`)
	cardMetaRe   = regexp.MustCompile(`<p class="card-subtitle[^"]*">\s*([A-Za-z]+ \d{1,2}, \d{4})\s*\|\s*Duration:\s*([\d:]+)`)
	cardCastRe   = regexp.MustCompile(`(?s)<h6 class="card-subtitle[^"]*">(.*?)</h6>`)
	cardThumbRe  = regexp.MustCompile(`<img class="card-img-top" src="([^"]+)"`)
	castLinkRe   = regexp.MustCompile(`(?s)<a href="[^"]*/models/[^"]*"[^>]*>(.*?)</a>`)
	sceneIDRe    = regexp.MustCompile(`/(\d+)/?$`)
	modelsSecRe  = regexp.MustCompile(`(?s)<h2[^>]*>\s*Videos with[^<]*</h2>(.*?)(?:<h2|$)`)
	detailTagsRe = regexp.MustCompile(`(?s)Tags:(.*?)</p>`)
	tagLinkRe    = regexp.MustCompile(`(?s)<a href="[^"]*/videos/[^"]*"[^>]*>(.*?)</a>`)
	detailDescRe = regexp.MustCompile(`(?s)<h2 class="font-weight-bold h2-responsive">[^<]*</h2>\s*<p>(.*?)</p>`)
	tagSuffixRe  = regexp.MustCompile(`\s*\([^()]*\)$`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
)

var dateLayouts = []string{"Jan 2, 2006"}

// Scraper implements scraper.StudioScraper for zexyvr.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the ZexyVR scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://zexyvr.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"zexyvr.com",
		"zexyvr.com/videos",
		"zexyvr.com/videos/{tag}",
		"zexyvr.com/models/{name}/{id}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one listing entry; everything but the synopsis and tags is on it.
type card struct {
	id         string
	path       string
	title      string
	date       time.Time
	duration   int
	performers []string
	thumb      string
}

// listing is what an operator URL selects: the whole catalogue, a tag page
// (both paginated) or a model page (a single page).
type listing struct {
	path  string
	model bool
}

func resolveListing(studioURL string) listing {
	u, err := url.Parse(studioURL)
	if err != nil {
		return listing{path: "/videos"}
	}
	p := strings.TrimRight(u.Path, "/")
	switch {
	case modelPathRe.MatchString(p):
		return listing{path: p, model: true}
	case tagPathRe.MatchString(p):
		return listing{path: p}
	}
	return listing{path: "/videos"}
}

func (s *Scraper) listingURL(l listing, page int) string {
	if l.model {
		return s.base + l.path
	}
	return fmt.Sprintf("%s%s?order=&page=%d", s.base, l.path, page)
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	l := resolveListing(studioURL)
	if l.model {
		scraper.Debugf(1, "zexyvr: scraping model page %s", l.path)
	} else {
		scraper.Debugf(1, "zexyvr: scraping listing %s", l.path)
	}

	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(l, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if l.model {
			body = modelVideosSection(body)
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 && !l.model {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}

		scenes := make([]models.Scene, 0, len(cards))
		var fresh []int
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			if !opts.KnownIDs[c.id] {
				fresh = append(fresh, len(scenes))
			}
			scenes = append(scenes, s.baseScene(c, studioURL, now))
		}
		s.enrich(ctx, scenes, fresh, opts, out)
		return scraper.PageResult{Scenes: scenes, Done: l.model}, nil
	})
}

// modelVideosSection cuts a model page down to its own videos; the page also
// lists photos and similar models after them.
func modelVideosSection(body string) string {
	if m := modelsSecRe.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

func parseListing(body string) []card {
	parts := cardSplitRe.Split(body, -1)
	cards := make([]card, 0, len(parts))
	for _, block := range parts[1:] {
		link := cardLinkRe.FindStringSubmatch(block)
		if link == nil {
			continue
		}
		path := link[1]
		if u, err := url.Parse(link[1]); err == nil {
			path = u.Path
		}
		id := sceneIDRe.FindStringSubmatch(path)
		if id == nil {
			continue
		}
		c := card{id: id[1], path: path, title: cleanText(link[2])}
		if m := cardMetaRe.FindStringSubmatch(block); m != nil {
			if d, err := parseutil.TryParseDate(m[1], dateLayouts...); err == nil {
				c.date = d
			}
			c.duration = parseutil.ParseDurationColon(m[2])
		}
		if m := cardCastRe.FindStringSubmatch(block); m != nil {
			for _, p := range castLinkRe.FindAllStringSubmatch(m[1], -1) {
				if name := cleanText(p[1]); name != "" {
					c.performers = append(c.performers, name)
				}
			}
		}
		if m := cardThumbRe.FindStringSubmatch(block); m != nil {
			c.thumb = html.UnescapeString(m[1])
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:         c.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     studioName,
		Title:      c.title,
		URL:        s.base + c.path,
		Date:       c.date,
		Duration:   c.duration,
		Performers: c.performers,
		Thumbnail:  c.thumb,
		ScrapedAt:  now,
	}
}

// enrich fetches the detail page of each scene in idx for its synopsis and tags.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, idx []int, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(idx) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "zexyvr: fetching %d details with %d workers", len(idx), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for _, i := range idx {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(&scenes[i], body)
		}(i)
	}
	wg.Wait()
}

func applyDetail(sc *models.Scene, body string) {
	if m := detailDescRe.FindStringSubmatch(body); m != nil {
		sc.Description = cleanText(m[1])
	}
	if m := detailTagsRe.FindStringSubmatch(body); m != nil {
		seen := map[string]bool{}
		for _, t := range tagLinkRe.FindAllStringSubmatch(m[1], -1) {
			tag := tagSuffixRe.ReplaceAllString(cleanText(t[1]), "")
			if tag != "" && !seen[tag] {
				seen[tag] = true
				sc.Tags = append(sc.Tags, tag)
			}
		}
	}
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}
