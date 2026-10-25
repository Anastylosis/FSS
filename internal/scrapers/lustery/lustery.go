package lustery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func init() { scraper.Register(New()) }

const (
	siteID = "lustery"
	// imgBase serves the resized thumbnail variants. The raw staticPath jpg
	// is not directly reachable, but the resize CDN is.
	imgBase = "https://img.lustery.com/cache/image/resize/width=1080/%s.convert.webp"
)

// Scraper scrapes the public Lustery scene catalogue via its JSON API.
type Scraper struct {
	client *http.Client
	base   string
}

// New constructs the Lustery scraper.
func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://lustery.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"lustery.com",
		"lustery.com/videos",
	}
}

var matchRe = regexp.MustCompile(`(?i)^https?://(?:www\.)?lustery\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// videoResponse is the shape of GET /api/video/{permalink}.
type videoResponse struct {
	Video videoDetail `json:"video"`
}

type videoDetail struct {
	Permalink          string   `json:"permalink"`
	Title              string   `json:"title"`
	CouplePermalink    string   `json:"couplePermalink"`
	CoupleName         string   `json:"coupleName"`
	CoupleDisplayed    string   `json:"coupleDisplayedName"`
	Categories         []string `json:"categories"`
	Tags               []string `json:"tags"`
	Duration           int      `json:"duration"`
	PublishAt          int64    `json:"publishAt"`
	FullPreviewSeconds int      `json:"fullPreviewDuration"`
	Poster             *poster  `json:"poster"`
}

type poster struct {
	StaticPath string `json:"staticPath"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()

	// /api/videos returns an empty list to anonymous clients since 2026-10,
	// so the catalogue is enumerated from the sitemap's /video/ entries. The
	// sitemap is not in release order: stored scenes are skipped, never
	// treated as the end of the walk.
	permalinks, err := s.fetchSitemap(ctx)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	if len(permalinks) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(s.base+"/sitemap.xml", errors.New("no /video/ entries"))))
		return
	}
	if !send(ctx, out, scraper.Progress(len(permalinks))) {
		return
	}

	fresh := permalinks[:0]
	skipped := false
	for _, pl := range permalinks {
		if opts.KnownIDs[pl] {
			skipped = true
			continue
		}
		fresh = append(fresh, pl)
	}
	s.fetchDetails(ctx, fresh, studioURL, opts, now, out)
	if skipped && ctx.Err() == nil {
		send(ctx, out, scraper.StoppedEarly())
	}
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

var sitemapVideoRe = regexp.MustCompile(`<loc>\s*https?://[^/<]+/video/([^/<\s]+)\s*</loc>`)

func (s *Scraper) fetchSitemap(ctx context.Context) ([]string, error) {
	u := s.base + "/sitemap.xml"
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range sitemapVideoRe.FindAllSubmatch(body, -1) {
		pl := string(m[1])
		if !seen[pl] {
			seen[pl] = true
			out = append(out, pl)
		}
	}
	return out, nil
}

func (s *Scraper) fetchDetails(ctx context.Context, permalinks []string, studioURL string, opts scraper.ListOpts, now time.Time, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, 4)
	scraper.Debugf(1, "lustery: fetching %d details with %d workers", len(permalinks), workers)

	jobs := make(chan string)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pl := range jobs {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				detail, err := s.fetchDetail(ctx, pl)
				var r scraper.SceneResult
				if err != nil {
					r = scraper.Error(fmt.Errorf("detail %s: %w", pl, err))
				} else {
					r = scraper.Scene(toScene(s.base, studioURL, detail, now))
				}
				if !send(ctx, out, r) {
					return
				}
			}
		}()
	}
feed:
	for _, pl := range permalinks {
		select {
		case jobs <- pl:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
}

func (s *Scraper) fetchDetail(ctx context.Context, permalink string) (videoDetail, error) {
	url := fmt.Sprintf("%s/api/video/%s", s.base, permalink)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return videoDetail{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	var vr videoResponse
	if err := httpx.DecodeJSON(resp.Body, &vr); err != nil {
		return videoDetail{}, err
	}
	return vr.Video, nil
}

func toScene(base, studioURL string, d videoDetail, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         d.Permalink,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      strings.TrimSpace(d.Title),
		URL:        base + "/video/" + d.Permalink,
		Studio:     "Lustery",
		Duration:   d.Duration,
		Tags:       mergeTags(d.Categories, d.Tags),
		Performers: performers(d),
		ScrapedAt:  now,
	}

	if d.PublishAt > 0 {
		scene.Date = time.Unix(d.PublishAt, 0).UTC()
	}

	if d.Poster != nil && d.Poster.StaticPath != "" {
		scene.Thumbnail = fmt.Sprintf(imgBase, d.Poster.StaticPath)
	}

	return scene
}

// performers derives full performer names. The couple permalink encodes the
// two full names joined by "-and-" (e.g. "iris-leon-and-jase-leon"), which is
// richer than coupleName ("Iris & Jase"). Fall back to splitting coupleName.
func performers(d videoDetail) []string {
	if d.CouplePermalink != "" {
		var out []string
		for _, part := range strings.Split(d.CouplePermalink, "-and-") {
			if name := titleizeSlug(part); name != "" {
				out = append(out, name)
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	name := d.CoupleName
	if name == "" {
		name = d.CoupleDisplayed
	}
	var out []string
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '&' || r == '+' }) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func titleizeSlug(slug string) string {
	words := strings.Split(strings.TrimSpace(slug), "-")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.TrimSpace(strings.Join(words, " "))
}

// mergeTags combines categories and tags, deduping while preserving order.
func mergeTags(categories, tags []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, group := range [][]string{categories, tags} {
		for _, t := range group {
			t = strings.TrimSpace(t)
			if t != "" && !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}
