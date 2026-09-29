// Package rawcitytwinks scrapes Raw City Twinks (rawcitytwinks.com), the NATS
// tour that also serves Black Rayne's Breed It Raw domain. See docs/scrapers.md.
package rawcitytwinks

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

const (
	siteID     = "rawcitytwinks"
	studioName = "Raw City Twinks"
	siteBase   = "https://www.rawcitytwinks.com"
	// maxModelPages bounds the performer lookup, which is one fetch per model.
	maxModelPages = 200
	// maxModelIndexPages bounds the model index walk.
	maxModelIndexPages = 50
)

var (
	matchRe     = regexp.MustCompile(`^https?://(?:www\.)?(?:rawcitytwinks\.com|breeditraw\.net)(?:/|$)`)
	linkRe      = regexp.MustCompile(`href="[^"]*?/tour/trailers/([^"]+)\.html"[^>]*title="([^"]*)"`)
	thumbRe     = regexp.MustCompile(`src0_1x="([^"]+)"`)
	timeRe      = regexp.MustCompile(`<div class="time">\s*([\d:]+)\s*</div>`)
	dateRe      = regexp.MustCompile(`<div class="date">\s*(\d{4}-\d{2}-\d{2})\s*</div>`)
	modelSlugRe = regexp.MustCompile(`/tour/models/([a-zA-Z0-9_-]+)\.html`)
	// A model page names its model only in an "About <name>" heading.
	modelNameRe = regexp.MustCompile(`(?is)<h\d[^>]*>\s*About\s+(.{2,40}?)\s*</h\d>`)
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"rawcitytwinks.com",
		"breeditraw.net",
		"rawcitytwinks.com/tour/models/{slug}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one listing entry. The tour's trailer pages carry no per-scene
// metadata, so everything a scene has comes from here plus the model pages.
type card struct {
	slug, title, thumb string
	duration           int
	date               time.Time
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if m := regexp.MustCompile(`/tour/models/([^"/]+)\.html`).FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "rawcitytwinks: scraping model page %s", m[1])
		s.runModel(ctx, studioURL, m[1], out)
		return
	}

	cards, err := s.walkListing(ctx, opts)
	if err != nil {
		s.send(ctx, out, scraper.Error(err))
		return
	}
	if len(cards) == 0 {
		return
	}
	s.send(ctx, out, scraper.Progress(len(cards)))

	cast := s.castBySlug(ctx, s.modelSlugs(ctx, opts), opts)
	now := time.Now().UTC()
	for _, c := range cards {
		scene := toScene(c, studioURL, now)
		scene.Performers = cast[c.slug]
		if !s.send(ctx, out, scraper.Scene(scene)) {
			return
		}
	}
}

// walkListing pages through the catalogue until a page yields no cards.
func (s *Scraper) walkListing(ctx context.Context, opts scraper.ListOpts) ([]card, error) {
	var cards []card
	seen := map[string]bool{}

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return cards, nil
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return cards, nil
		}
		pageURL := fmt.Sprintf("%s/tour/categories/movies/%d/latest/", s.base, page)
		scraper.Debugf(1, "rawcitytwinks: fetching page %d", page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return cards, fmt.Errorf("page %d: %w", page, err)
		}
		found := parseCards(body)
		if len(found) == 0 {
			return cards, nil
		}
		fresh := 0
		for _, c := range found {
			if seen[c.slug] {
				continue
			}
			seen[c.slug] = true
			cards = append(cards, c)
			fresh++
		}
		// A tour that clamps past its last page re-serves the previous one;
		// without this the walk never ends.
		if fresh == 0 {
			return cards, nil
		}
	}
}

// runModel scrapes a single model page, which lists only that model's scenes.
func (s *Scraper) runModel(ctx context.Context, studioURL, slug string, out chan<- scraper.SceneResult) {
	body, err := s.fetchPage(ctx, fmt.Sprintf("%s/tour/models/%s.html", s.base, slug))
	if err != nil {
		s.send(ctx, out, scraper.Error(err))
		return
	}
	cards := parseCards(body)
	s.send(ctx, out, scraper.Progress(len(cards)))
	now := time.Now().UTC()
	name := modelName(body)
	for _, c := range cards {
		scene := toScene(c, studioURL, now)
		if name != "" {
			scene.Performers = []string{name}
		}
		if !s.send(ctx, out, scraper.Scene(scene)) {
			return
		}
	}
}

// modelSlugs walks the paginated model index, which is the only place the tour
// lists its models — the scene listing links none.
func (s *Scraper) modelSlugs(ctx context.Context, opts scraper.ListOpts) []string {
	seen := map[string]bool{}
	var slugs []string
	for page := 1; page <= maxModelIndexPages; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return slugs
		}
		body, err := s.fetchPage(ctx, fmt.Sprintf("%s/tour/models/%d/popular/", s.base, page))
		if err != nil {
			break
		}
		fresh := 0
		for slug := range parseModelSlugs(body) {
			if seen[slug] {
				continue
			}
			seen[slug] = true
			slugs = append(slugs, slug)
			fresh++
		}
		if fresh == 0 {
			break
		}
	}
	scraper.Debugf(1, "rawcitytwinks: model index yielded %d models", len(slugs))
	return slugs
}

// castBySlug maps each scene slug to its performers by fetching every model
// page once — the trailer pages name no cast, and the listing cards do not either.
func (s *Scraper) castBySlug(ctx context.Context, slugs []string, opts scraper.ListOpts) map[string][]string {
	cast := map[string][]string{}
	if len(slugs) > maxModelPages {
		slugs = slugs[:maxModelPages]
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = 3
	}
	scraper.Debugf(1, "rawcitytwinks: resolving cast from %d model pages with %d workers", len(slugs), workers)

	var mu sync.Mutex
	var wg sync.WaitGroup
	work := make(chan string)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for slug := range work {
				body, err := s.fetchPage(ctx, fmt.Sprintf("%s/tour/models/%s.html", s.base, slug))
				if err != nil {
					continue
				}
				name := modelName(body)
				if name == "" {
					continue
				}
				mu.Lock()
				for _, c := range parseCards(body) {
					cast[c.slug] = appendUnique(cast[c.slug], name)
				}
				mu.Unlock()
			}
		}()
	}
	for _, slug := range slugs {
		select {
		case work <- slug:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return cast
		}
	}
	close(work)
	wg.Wait()
	return cast
}

func toScene(c card, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:        c.slug,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     c.title,
		URL:       fmt.Sprintf("%s/tour/trailers/%s.html", siteBase, c.slug),
		Date:      c.date,
		Duration:  c.duration,
		Thumbnail: absURL(c.thumb),
		ScrapedAt: now,
	}
}

// parseCards reads the listing cards. Each is self-contained: link, title,
// thumbnail, runtime and date.
func parseCards(body string) []card {
	var out []card
	seen := map[string]bool{}
	for _, block := range splitCards(body) {
		m := linkRe.FindStringSubmatch(block)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		c := card{slug: m[1], title: strings.TrimSpace(html.UnescapeString(m[2]))}
		if t := thumbRe.FindStringSubmatch(block); t != nil {
			c.thumb = t[1]
		}
		if t := timeRe.FindStringSubmatch(block); t != nil {
			c.duration = parseutil.ParseDurationColon(t[1])
		}
		if d := dateRe.FindStringSubmatch(block); d != nil {
			if parsed, err := parseutil.TryParseDate(d[1], "2006-01-02"); err == nil {
				c.date = parsed.UTC()
			}
		}
		out = append(out, c)
	}
	return out
}

// splitCards cuts the page into one chunk per item-video block.
func splitCards(body string) []string {
	parts := strings.Split(body, `<div class="item-video`)
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}

// parseModelSlugs returns the model slugs linked on a page.
func parseModelSlugs(body string) map[string]bool {
	out := map[string]bool{}
	for _, m := range modelSlugRe.FindAllStringSubmatch(body, -1) {
		out[m[1]] = true
	}
	return out
}

// modelName reads the model a page belongs to from its "About <name>" heading.
func modelName(body string) string {
	if m := modelNameRe.FindStringSubmatch(body); m != nil {
		return strings.TrimSpace(html.UnescapeString(m[1]))
	}
	return ""
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) (string, error) {
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

func (s *Scraper) send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func absURL(u string) string {
	switch {
	case u == "":
		return ""
	case strings.HasPrefix(u, "http"):
		return u
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	case strings.HasPrefix(u, "/"):
		return siteBase + u
	default:
		return siteBase + "/tour/" + u
	}
}

func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if strings.EqualFold(existing, v) {
			return list
		}
	}
	return append(list, v)
}
