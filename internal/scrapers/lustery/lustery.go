package lustery

import (
	"context"
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
	siteID   = "lustery"
	pageSize = 24
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

// listResponse is the shape of GET /api/videos?page=N.
type listResponse struct {
	CurrentPagePermalinks []string `json:"currentPagePermalinks"`
	TotalCount            int      `json:"totalCount"`
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

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		list, err := s.fetchList(ctx, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if len(list.CurrentPagePermalinks) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			total = list.TotalCount
		}

		scenes := s.fetchDetails(ctx, list.CurrentPagePermalinks, studioURL, opts, now)

		done := list.TotalCount > 0 && page*pageSize >= list.TotalCount

		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   done,
		}, nil
	})
}

func (s *Scraper) fetchList(ctx context.Context, page int) (listResponse, error) {
	url := fmt.Sprintf("%s/api/videos?page=%d", s.base, page)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return listResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	var lr listResponse
	if err := httpx.DecodeJSON(resp.Body, &lr); err != nil {
		return listResponse{}, err
	}
	return lr, nil
}

func (s *Scraper) fetchDetails(ctx context.Context, permalinks []string, studioURL string, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	scraper.Debugf(1, "lustery: fetching %d details with %d workers", len(permalinks), workers)

	results := make([]models.Scene, len(permalinks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, pl := range permalinks {
		if ctx.Err() != nil {
			break
		}
		// Known IDs become lightweight stubs so Paginate's early-stop fires
		// without a detail fetch. The list is newest-first.
		if opts.KnownIDs[pl] {
			results[i] = models.Scene{ID: pl, SiteID: siteID}
			continue
		}
		wg.Add(1)
		go func(idx int, permalink string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if !scraper.Pace(ctx, opts.Delay) {
				// Leaving results[idx] unwritten is deliberate: the zero value has an
				// empty ID and the consumer below skips those, so a cancelled fetch
				// contributes no scene. Do not "fix" this by writing a stub here —
				// a populated slot is emitted as a real scene with no title or URL.
				return
			}

			detail, err := s.fetchDetail(ctx, permalink)
			if err != nil {
				scraper.Debugf(1, "lustery: detail %s failed: %v (skipping)", permalink, err)
				return
			}
			results[idx] = toScene(s.base, studioURL, detail, now)
		}(i, pl)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, sc := range results {
		if sc.ID == "" { // failed fetch
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
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
