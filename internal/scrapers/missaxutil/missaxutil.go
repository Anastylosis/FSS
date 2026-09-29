package missaxutil

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

type SiteConfig struct {
	SiteID string
	Domain string
	Studio string
}

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/tour/models/{name}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool {
	return s.matchRe.MatchString(u)
}

var modelRe = regexp.MustCompile(`/tour/models/`)

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listEntry struct {
	id         string
	title      string
	url        string
	thumbnail  string
	performers []string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	work := make(chan listEntry, opts.Workers)
	var wg sync.WaitGroup
	scraper.Debugf(1, "%s: fetching detail pages with %d workers", s.cfg.SiteID, opts.Workers)

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, studioURL, entry)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	if modelRe.MatchString(studioURL) {
		scraper.Debugf(1, "%s: detected model page", s.cfg.SiteID)
		s.produceModel(ctx, studioURL, opts, out, work)
	} else {
		s.produceListing(ctx, opts, out, work)
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) produceModel(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listEntry) {
	entries, err := s.fetchURL(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	if len(entries) > 0 {
		scraper.Debugf(1, "%s: found %d scenes on model page", s.cfg.SiteID, len(entries))
		select {
		case out <- scraper.Progress(len(entries)):
		case <-ctx.Done():
			return
		}
	}
	for _, e := range entries {
		if opts.KnownIDs[e.id] {
			continue
		}
		select {
		case work <- e:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) produceListing(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listEntry) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			break
		}

		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)
		entries, err := s.fetchPage(ctx, page)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		if len(entries) == 0 {
			break
		}

		if page == 1 {
			scraper.Debugf(1, "%s: %d total scenes (estimated)", s.cfg.SiteID, estimateTotal(len(entries)))
			cancelled := false
			select {
			case out <- scraper.Progress(estimateTotal(len(entries))):
			case <-ctx.Done():
				cancelled = true
			}
			if cancelled {
				break
			}
		}

		cancelled := false
		hitKnown := false
		for _, e := range entries {
			if opts.KnownIDs[e.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.SiteID, e.id)
				hitKnown = true
				break
			}
			select {
			case work <- e:
			case <-ctx.Done():
				cancelled = true
			}
			if cancelled {
				break
			}
		}
		if cancelled || hitKnown {
			if hitKnown {
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
			}
			break
		}
	}
}

func estimateTotal(firstPageCount int) int {
	return firstPageCount * 54
}

var (
	sceneBlockRe = regexp.MustCompile(`(?s)photo-thumb video-thumb">(.*?)<!-- end photo-thumb -->`)
	sceneLinkRe  = regexp.MustCompile(`href="(https?://[^"]*?/tour/trailers/[^"]+\.html)"`)
	sceneTitleRe = regexp.MustCompile(`title="([^"]+)"`)
	sceneIDRe    = regexp.MustCompile(`contentthumbs/\d+/\d+/(\d+)-`)
	thumbRe      = regexp.MustCompile(`src0_1x="([^"]+)"`)
	performersRe = regexp.MustCompile(`(?s)class="model-name">\s*(.*?)\s*</p>`)
	performerRe  = regexp.MustCompile(`>([^<]+)</a>`)
)

func (s *Scraper) fetchURL(ctx context.Context, pageURL string) ([]listEntry, error) {
	body, err := s.fetchBody(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	return parsePage(body), nil
}

func (s *Scraper) fetchPage(ctx context.Context, page int) ([]listEntry, error) {
	return s.fetchURL(ctx, fmt.Sprintf("%s/tour/categories/movies_%d_d.html", s.base, page))
}

func parsePage(body []byte) []listEntry {
	blocks := sceneBlockRe.FindAllSubmatch(body, -1)
	entries := make([]listEntry, 0, len(blocks))

	for _, block := range blocks {
		content := block[1]

		linkM := sceneLinkRe.FindSubmatch(content)
		if linkM == nil {
			continue
		}

		entry := listEntry{
			url: string(linkM[1]),
		}

		if m := sceneTitleRe.FindSubmatch(content); m != nil {
			entry.title = html.UnescapeString(string(m[1]))
		}
		if m := sceneIDRe.FindSubmatch(content); m != nil {
			entry.id = string(m[1])
		}
		if m := thumbRe.FindSubmatch(content); m != nil {
			entry.thumbnail = string(m[1])
		}
		if m := performersRe.FindSubmatch(content); m != nil {
			for _, pm := range performerRe.FindAllSubmatch(m[1], -1) {
				entry.performers = append(entry.performers, strings.TrimSpace(string(pm[1])))
			}
		}

		if entry.id == "" {
			entry.id = slugFromURL(entry.url)
		}

		entries = append(entries, entry)
	}

	return entries
}

var (
	runtimeRe     = regexp.MustCompile(`Runtime:\s*(\d+:\d+(?::\d+)?)`)
	addedRe       = regexp.MustCompile(`Added:\s*(\d{2}/\d{2}/\d{4})`)
	categoriesRe  = regexp.MustCompile(`(?s)Categories:\s*(.*?)</p>`)
	categoryRe    = regexp.MustCompile(`>([^<]+)</a>`)
	videoSrcRe    = regexp.MustCompile(`<video\s+src="([^"]+)"`)
	descriptionRe = regexp.MustCompile(`(?s)Video Description:\s*</p>\s*<p[^>]*>\s*(.*?)\s*</p>`)
	htmlTagRe     = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) fetchDetail(ctx context.Context, studioURL string, entry listEntry) (models.Scene, error) {
	body, err := s.fetchBody(ctx, entry.url)
	if err != nil {
		return models.Scene{}, err
	}

	now := time.Now().UTC()

	scene := models.Scene{
		ID:         entry.id,
		SiteID:     s.cfg.SiteID,
		StudioURL:  studioURL,
		Title:      entry.title,
		URL:        entry.url,
		Thumbnail:  entry.thumbnail,
		Performers: entry.performers,
		Studio:     s.cfg.Studio,
		ScrapedAt:  now,
	}

	if m := runtimeRe.FindSubmatch(body); m != nil {
		scene.Duration = parseutil.ParseDurationColon(string(m[1]))
	}

	if m := addedRe.FindSubmatch(body); m != nil {
		scene.Date = parseDate(string(m[1]))
	}

	if m := categoriesRe.FindSubmatch(body); m != nil {
		for _, cm := range categoryRe.FindAllSubmatch(m[1], -1) {
			scene.Tags = append(scene.Tags, strings.TrimSpace(string(cm[1])))
		}
	}

	if m := descriptionRe.FindSubmatch(body); m != nil {
		desc := htmlTagRe.ReplaceAll(m[1], []byte("\n"))
		desc = []byte(html.UnescapeString(string(desc)))
		desc = []byte(strings.TrimSpace(string(desc)))
		scene.Description = collapseWhitespace(string(desc))
	}

	if m := videoSrcRe.FindSubmatch(body); m != nil {
		src := string(m[1])
		if !strings.HasPrefix(src, "http") {
			src = s.base + src
		}
		scene.Preview = src
	}

	if v := parseutil.OpenGraph(body)["og:image"]; v != "" {
		scene.Thumbnail = v
	}

	return scene, nil
}

func (s *Scraper) fetchBody(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func parseDate(s string) time.Time {
	t, err := time.Parse("01/02/2006", s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func slugFromURL(u string) string {
	u = strings.TrimSuffix(u, ".html")
	if idx := strings.LastIndex(u, "/"); idx >= 0 {
		u = u[idx+1:]
	}
	return u
}

var multiNewlineRe = regexp.MustCompile(`\n{3,}`)

func collapseWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	s = strings.Join(lines, "\n")
	return multiNewlineRe.ReplaceAllString(s, "\n\n")
}
