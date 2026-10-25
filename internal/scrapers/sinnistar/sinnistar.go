// Package sinnistar scrapes sinnistar.com, a hand-built static tour whose
// scenes are only described on one page per model. See docs/scrapers.md.
package sinnistar

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "sinnistar"
	studioName    = "Sinnistar"
	defaultBase   = "https://sinnistar.com"
	modelsPath    = "/sinnistar-models.html"
	detailWorkers = 2
)

var (
	matchRe     = regexp.MustCompile(`^https?://(?:www\.)?sinnistar\.com(?:[/?#]|$)`)
	modelListRe = regexp.MustCompile(`(?s)<ul class="model nav nav-pills">(.*?)</ul>`)
	anchorRe    = regexp.MustCompile(`(?s)<a\b[^>]*href="([^"]+\.html)"[^>]*>(.*?)</a>`)
	h2Re        = regexp.MustCompile(`(?s)<h2>(.*?)</h2>`)
	posterRe    = regexp.MustCompile(`poster="([^"]+)"`)
	sourceRe    = regexp.MustCompile(`<source\s+src="([^"]+)"`)
	notesRe     = regexp.MustCompile(`(?s)class="row notes">(.*?)</div>`)
	paraRe      = regexp.MustCompile(`(?s)<p>(.*?)</p>`)
	tagRe       = regexp.MustCompile(`<[^>]*>`)
	spaceRe     = regexp.MustCompile(`\s+`)
)

// catalogPages are the site's own index pages: each one stands for the whole
// catalogue rather than a filtered view.
var catalogPages = map[string]bool{
	"": true, "/": true, "/index.html": true, "/tour.html": true,
	"/tour2.html": true, modelsPath: true,
}

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: defaultBase}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"sinnistar.com",
		"sinnistar.com/tour.html",
		"sinnistar.com/sinnistar-models.html",
		"sinnistar.com/{model}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type model struct {
	name string
	path string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	indexURL := s.base + modelsPath
	body, err := s.fetch(ctx, indexURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	list := parseModels(body)
	if len(list) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(indexURL, fmt.Errorf("no model links"))))
		return
	}

	pagePath := ""
	if u, err := url.Parse(studioURL); err == nil {
		pagePath = u.Path
	}
	if !catalogPages[pagePath] {
		var picked []model
		for _, m := range list {
			if m.path == pagePath {
				picked = append(picked, m)
			}
		}
		if len(picked) == 0 {
			send(ctx, out, scraper.Error(fmt.Errorf("%s: %s is not a model page", siteID, studioURL)))
			return
		}
		scraper.Debugf(1, "%s: scraping model page %s", siteID, pagePath)
		list = picked
	}

	scenes, ok := s.collect(ctx, list, studioURL, opts, out)
	if !ok {
		return
	}
	if !send(ctx, out, scraper.Progress(len(scenes))) {
		return
	}
	for _, sc := range scenes {
		if !send(ctx, out, scraper.Scene(sc)) {
			return
		}
	}
}

// collect fetches every model page and folds the scenes together. A scene
// shot with two models appears on both pages, so it is emitted once with both
// pages' performers; that is why nothing is sent until every page is in.
func (s *Scraper) collect(ctx context.Context, list []model, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) ([]models.Scene, bool) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d model pages with %d workers", siteID, len(list), workers)

	pages := make([][]models.Scene, len(list))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				pages[i] = s.fetchModel(ctx, list[i], studioURL, opts, out)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := range list {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	if ctx.Err() != nil {
		return nil, false
	}
	return merge(pages), true
}

func merge(pages [][]models.Scene) []models.Scene {
	var scenes []models.Scene
	index := map[string]int{}
	for _, page := range pages {
		for _, sc := range page {
			i, seen := index[sc.ID]
			if !seen {
				index[sc.ID] = len(scenes)
				scenes = append(scenes, sc)
				continue
			}
			for _, p := range sc.Performers {
				if !contains(scenes[i].Performers, p) {
					scenes[i].Performers = append(scenes[i].Performers, p)
				}
			}
		}
	}
	return scenes
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

func (s *Scraper) fetchModel(ctx context.Context, m model, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) []models.Scene {
	if !scraper.Pace(ctx, opts.Delay) {
		return nil
	}
	pageURL := s.base + m.path
	body, err := s.fetch(ctx, pageURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return nil
	}
	scenes := parseModelPage(body, pageURL, studioURL, performerName(m.name), time.Now().UTC())
	if len(scenes) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(pageURL, fmt.Errorf("no scene blocks"))))
	}
	return scenes
}

func parseModels(body []byte) []model {
	m := modelListRe.FindSubmatch(body)
	if m == nil {
		return nil
	}
	var list []model
	seen := map[string]bool{}
	for _, a := range anchorRe.FindAllSubmatch(m[1], -1) {
		u, err := url.Parse(html.UnescapeString(string(a[1])))
		if err != nil || seen[u.Path] {
			continue
		}
		seen[u.Path] = true
		list = append(list, model{name: cleanText(string(a[2])), path: u.Path})
	}
	return list
}

// performerName turns a model-index label into a credit. The index prefixes
// a later shoot with "Pregnant", which is not part of the name. A label with
// a slash names either two models ("Amber/Carmen") or one model and her alias
// ("Stacey Cash/Ashley Stephenson"), and nothing on the page says which, so
// it is not credited.
func performerName(label string) string {
	name := strings.TrimSpace(strings.TrimPrefix(label, "Pregnant "))
	if strings.Contains(name, "/") {
		return ""
	}
	return name
}

func parseModelPage(body []byte, pageURL, studioURL, performer string, now time.Time) []models.Scene {
	blocks := strings.Split(string(body), `class="logoH1"`)
	var scenes []models.Scene
	for _, b := range blocks[1:] {
		src := sourceRe.FindStringSubmatch(b)
		h := h2Re.FindStringSubmatch(b)
		if src == nil || h == nil {
			continue
		}
		id := strings.TrimSuffix(path.Base(html.UnescapeString(src[1])), path.Ext(src[1]))
		title := cleanText(h[1])
		if id == "" || title == "" {
			continue
		}
		sc := models.Scene{
			ID:        id,
			SiteID:    siteID,
			StudioURL: studioURL,
			Studio:    studioName,
			Title:     title,
			URL:       pageURL,
			ScrapedAt: now,
		}
		if p := posterRe.FindStringSubmatch(b); p != nil {
			sc.Thumbnail = html.UnescapeString(p[1])
		}
		if n := notesRe.FindStringSubmatch(b); n != nil {
			var paras []string
			for _, p := range paraRe.FindAllStringSubmatch(n[1], -1) {
				if t := cleanText(p[1]); t != "" {
					paras = append(paras, t)
				}
			}
			sc.Description = strings.Join(paras, "\n\n")
		}
		if performer != "" {
			sc.Performers = []string{performer}
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(spaceRe.ReplaceAllString(html.UnescapeString(s), " "))
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) ([]byte, error) {
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
