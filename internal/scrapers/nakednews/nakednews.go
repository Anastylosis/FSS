package nakednews

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type Scraper struct {
	client *http.Client
}

// RecommendedDelay is the pace this scraper was written against. It is **not**
// silently enforced — the operator's `opts.Delay` is always honoured, including
// `--delay 0` — but `WarnDelayBelow` surfaces a one-shot stderr warning below it.
const RecommendedDelay = 500 * time.Millisecond

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "nakednews" }

func (s *Scraper) Patterns() []string {
	return []string{
		"nakednews.com/archives",
		"nakednews.com/archives?segmentid={id}",
		"nakednews.com/archives?anchorid={id}",
		"nakednews.com/naked-news-anchor-{slug}-a{id}",
		"nakednews.com/{year}/{month}",
		"nakednews.com/auditions",
		"nakednews.com/clip-store",
	}
}

var (
	archivesRe  = regexp.MustCompile(`^https?://(?:www\.)?nakednews\.com/archives`)
	anchorRe    = regexp.MustCompile(`^https?://(?:www\.)?nakednews\.com/naked-news-anchor-.*-a(\d+)`)
	dateRe      = regexp.MustCompile(`^https?://(?:www\.)?nakednews\.com/(\d{4})/(\d{2})`)
	auditionsRe = regexp.MustCompile(`^https?://(?:www\.)?nakednews\.com/auditions`)
	clipStoreRe = regexp.MustCompile(`^https?://(?:www\.)?nakednews\.com/clip-store`)
	homeRe      = regexp.MustCompile(`^https?://(?:www\.)?nakednews\.com/?$`)
)

func (s *Scraper) MatchesURL(u string) bool {
	return archivesRe.MatchString(u) ||
		anchorRe.MatchString(u) ||
		dateRe.MatchString(u) ||
		auditionsRe.MatchString(u) ||
		clipStoreRe.MatchString(u) ||
		homeRe.MatchString(u)
}

// ---- API types ----

type listItem struct {
	ProgramSegmentID int    `json:"programSegmentId"`
	Date             int64  `json:"date"`
	Title            string `json:"title"`
	Image            string `json:"image"`
	Slug             string `json:"slug"`
}

type listResponse struct {
	Segments []listItem `json:"segments"`
	Count    int        `json:"count"`
}

type featuredResponse struct {
	Content      []listItem `json:"content"`
	TotalContent int        `json:"totalContent"`
}

type detail struct {
	Description string         `json:"description"`
	Segment     detailSegment  `json:"segment"`
	Clip        detailClip     `json:"clip"`
	Anchors     []detailAnchor `json:"anchors"`
	LikesCount  int            `json:"likesCount"`
	Tags        []detailTag    `json:"tags"`
}

type detailSegment struct {
	Name string `json:"name"`
}

type detailClip struct {
	ImageURL string `json:"imageUrl"`
}

type detailAnchor struct {
	Name string `json:"name"`
}

type detailTag struct {
	Name string `json:"name"`
}

// ---- list config ----

type fetchMode int

const (
	modeAll fetchMode = iota
	modeSegmentType
	modeAnchor
	modeDate
	modeAuditions
	modeFeatured
)

type listConfig struct {
	mode  fetchMode
	param string
	year  string
	month string
}

var (
	anchorPathRe = regexp.MustCompile(`^/naked-news-anchor-.*-a(\d+)`)
	datePathRe   = regexp.MustCompile(`^/(\d{4})/(\d{2})`)
)

func parseMode(studioURL string) (listConfig, error) {
	u, err := url.Parse(studioURL)
	if err != nil {
		return listConfig{}, err
	}
	path := u.Path
	q := u.Query()

	if m := anchorPathRe.FindStringSubmatch(path); m != nil {
		return listConfig{mode: modeAnchor, param: m[1]}, nil
	}
	if strings.HasPrefix(path, "/archives") {
		if sid := q.Get("segmentid"); sid != "" {
			return listConfig{mode: modeSegmentType, param: sid}, nil
		}
		if aid := q.Get("anchorid"); aid != "" {
			return listConfig{mode: modeAnchor, param: aid}, nil
		}
		return listConfig{mode: modeAll}, nil
	}
	if m := datePathRe.FindStringSubmatch(path); m != nil {
		monthInt, _ := strconv.Atoi(m[2])
		return listConfig{mode: modeDate, year: m[1], month: strconv.Itoa(monthInt)}, nil
	}
	if path == "/auditions" {
		return listConfig{mode: modeAuditions}, nil
	}
	if path == "/clip-store" {
		return listConfig{mode: modeFeatured}, nil
	}
	if path == "/" || path == "" {
		return listConfig{mode: modeAll}, nil
	}
	return listConfig{}, fmt.Errorf("unrecognized URL: %s", studioURL)
}

// ---- scraper interface ----

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if _, err := parseMode(studioURL); err != nil {
		return nil, err
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

const (
	pageSize = 100
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	delay := opts.Delay
	scraper.WarnDelayBelow("nakednews", opts.Delay, RecommendedDelay)

	cfg, _ := parseMode(studioURL)
	base := apiBase(studioURL)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listItem)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				scene, err := s.fetchDetail(ctx, studioURL, base, item, delay)
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)

		seen := make(map[int]bool)

		for page := 0; ; page++ {
			if page > 0 && !scraper.Pace(ctx, delay) {
				return
			}
			scraper.Debugf(1, "nakednews: fetching page %d", page)

			items, total, err := s.fetchPage(ctx, base, cfg, page)
			if err != nil {
				select {
				case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
				case <-ctx.Done():
				}
				return
			}

			if len(items) == 0 {
				return
			}

			if page == 0 {
				t := total
				if t <= 0 {
					t = len(items)
				}
				scraper.Debugf(1, "nakednews: %d total scenes", t)
				select {
				case out <- scraper.Progress(t):
				case <-ctx.Done():
					return
				}
			}

			newCount := 0
			for _, item := range items {
				if item.ProgramSegmentID == 0 || seen[item.ProgramSegmentID] {
					continue
				}
				seen[item.ProgramSegmentID] = true
				newCount++

				id := strconv.Itoa(item.ProgramSegmentID)
				if opts.KnownIDs[id] {
					scraper.Debugf(1, "nakednews: hit known ID, stopping early")
					select {
					case out <- scraper.StoppedEarly():
					case <-ctx.Done():
					}
					return
				}

				select {
				case work <- item:
				case <-ctx.Done():
					return
				}
			}

			if total > 0 && (page+1)*pageSize >= total {
				return
			}
			if total <= 0 && (len(items) < pageSize || newCount == 0) {
				return
			}
		}
	}()

	wg.Wait()
}

// ---- detail fetching ----

func (s *Scraper) fetchDetail(ctx context.Context, studioURL, base string, item listItem, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	var d detail
	apiURL := fmt.Sprintf("%s/v1/program/programSegment/%d", base, item.ProgramSegmentID)
	if err := s.fetchJSON(ctx, apiURL, &d); err != nil {
		return models.Scene{}, fmt.Errorf("detail %d: %w", item.ProgramSegmentID, err)
	}

	return toScene(studioURL, item, &d), nil
}

// ---- scene mapping ----

func toScene(studioURL string, item listItem, d *detail) models.Scene {
	scene := models.Scene{
		ID:        strconv.Itoa(item.ProgramSegmentID),
		SiteID:    "nakednews",
		StudioURL: studioURL,
		Title:     item.Title,
		URL:       buildSceneURL(studioURL, item.Slug),
		Thumbnail: item.Image,
		Studio:    "Naked News",
		ScrapedAt: time.Now().UTC(),
	}

	if item.Date > 0 {
		scene.Date = time.UnixMilli(item.Date).UTC()
	}

	if d != nil {
		scene.Description = d.Description
		scene.Likes = d.LikesCount
		for _, a := range d.Anchors {
			if a.Name != "" {
				scene.Performers = append(scene.Performers, a.Name)
			}
		}
		for _, t := range d.Tags {
			if t.Name != "" {
				scene.Tags = append(scene.Tags, t.Name)
			}
		}
		if d.Segment.Name != "" {
			scene.Categories = append(scene.Categories, d.Segment.Name)
		}
		if d.Clip.ImageURL != "" {
			scene.Thumbnail = d.Clip.ImageURL
		}
	}

	return scene
}

func buildSceneURL(studioURL, slug string) string {
	u, _ := url.Parse(studioURL)
	return u.Scheme + "://" + u.Host + "/" + slug
}

// ---- HTTP ----

func apiBase(studioURL string) string {
	u, _ := url.Parse(studioURL)
	return u.Scheme + "://" + u.Host + "/api/rest"
}

func (s *Scraper) fetchJSON(ctx context.Context, rawURL string, v any) error {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: rawURL,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Accept"] = "application/json"
			return h
		}(),
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.DecodeJSON(resp.Body, v)
}

func (s *Scraper) fetchPage(ctx context.Context, base string, cfg listConfig, page int) ([]listItem, int, error) {
	var apiURL string
	switch cfg.mode {
	case modeAll:
		apiURL = fmt.Sprintf("%s/v1/program?page=%d&size=%d", base, page, pageSize)
	case modeSegmentType:
		apiURL = fmt.Sprintf("%s/v1/program/segment/%s?page=%d&size=%d", base, cfg.param, page, pageSize)
	case modeAnchor:
		apiURL = fmt.Sprintf("%s/v1/anchor/%s/segment?page=%d&size=%d", base, cfg.param, page, pageSize)
	case modeDate:
		apiURL = fmt.Sprintf("%s/v1/program/date?page=%d&size=%d&year=%s&month=%s", base, page, pageSize, cfg.year, cfg.month)
	case modeAuditions:
		apiURL = fmt.Sprintf("%s/v1/audition?page=%d&size=%d", base, page, pageSize)
	case modeFeatured:
		apiURL = fmt.Sprintf("%s/v2/featured?page=%d&size=%d", base, page, pageSize)
	}

	switch cfg.mode {
	case modeAnchor:
		var items []listItem
		if err := s.fetchJSON(ctx, apiURL, &items); err != nil {
			return nil, 0, err
		}
		return items, -1, nil
	case modeFeatured:
		var fr featuredResponse
		if err := s.fetchJSON(ctx, apiURL, &fr); err != nil {
			return nil, 0, err
		}
		return fr.Content, fr.TotalContent, nil
	default:
		var lr listResponse
		if err := s.fetchJSON(ctx, apiURL, &lr); err != nil {
			return nil, 0, err
		}
		return lr.Segments, lr.Count, nil
	}
}
