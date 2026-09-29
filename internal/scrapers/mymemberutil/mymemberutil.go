package mymemberutil

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const apiPath = "/api/cancellable-request"

// SiteConfig defines a MyMember.site instance.
type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	// BasePath is the path the instance is mounted at, with a leading and no
	// trailing slash. Empty means the domain root; a site that never took its
	// own domain is served under the platform's (CandyXS at
	// mymember.site/candyxs).
	BasePath        string
	KnownPerformers map[string]bool
}

// Scraper handles listing and detail fetching for a MyMember.site instance.
type Scraper struct {
	cfg      SiteConfig
	Client   *http.Client
	SiteBase string
}

// New creates a Scraper for the given site configuration.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:      cfg,
		Client:   httpx.NewClient(30 * time.Second),
		SiteBase: "https://" + cfg.Domain + cfg.BasePath,
	}
}

// Config returns the site configuration.
func (s *Scraper) Config() SiteConfig { return s.cfg }

type apiResponse struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
}

// VideosPage is the paginated list of videos returned by fetchVideosApi.
type VideosPage struct {
	CurrentPage int        `json:"current_page"`
	LastPage    int        `json:"last_page"`
	Total       int        `json:"total"`
	PerPage     int        `json:"per_page"`
	Data        []APIVideo `json:"data"`
}

// APIVideo is a single video entry from the listing API.
type APIVideo struct {
	ID                        int    `json:"id"`
	Title                     string `json:"title"`
	IsPublished               bool   `json:"is_published"`
	PublishDate               string `json:"publish_date"`
	Duration                  int    `json:"duration"`
	ContentMappingID          int    `json:"content_mapping_id"`
	ViewsCount                int    `json:"views_count"`
	LikesCount                int    `json:"likes_count"`
	CommentsCount             int    `json:"comments_count"`
	StreamPrice               any    `json:"stream_price"`
	PosterSrc                 string `json:"poster_src"`
	SystemPreviewVideoFullSrc string `json:"system_preview_video_full_src"`
	Has4K                     bool   `json:"has_4k"`
	IsVRVideo                 bool   `json:"is_vr_video"`
}

// Price extracts the numeric price from the stream_price field.
func (v APIVideo) Price() (float64, bool) {
	switch p := v.StreamPrice.(type) {
	case float64:
		return p, true
	case string:
		f, err := strconv.ParseFloat(p, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// Run is the main scraping goroutine. It must be called with go; out must be
// closed by the caller after Run returns.
func (s *Scraper) Run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := opts.Workers
	if workers <= 0 {
		workers = 3
	}

	work := make(chan APIVideo, workers)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for vid := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.BuildScene(ctx, studioURL, vid)
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

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			break
		}
		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)

		vp, err := s.FetchPage(ctx, page)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		if page == 1 {
			select {
			case out <- scraper.Progress(vp.Total):
			case <-ctx.Done():
			}
		}

		if len(vp.Data) == 0 {
			break
		}

		cancelled := false
		hitKnown := false
		for _, vid := range vp.Data {
			if !vid.IsPublished {
				continue
			}
			id := strconv.Itoa(vid.ID)
			if opts.KnownIDs[id] {
				hitKnown = true
				break
			}
			select {
			case work <- vid:
			case <-ctx.Done():
				cancelled = true
			}
			if cancelled {
				break
			}
		}
		if cancelled || hitKnown {
			if hitKnown {
				scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
			}
			break
		}

		if page >= vp.LastPage {
			break
		}
	}

	close(work)
	wg.Wait()
}

// apiHeaders shapes the request as the XHR it actually is. The platform's WAF
// started answering 403 to navigation-shaped headers on this endpoint — all
// three sites broke at once, and `Sec-Fetch-Dest: document` on an API call is
// what it objects to — so the fetch-metadata headers say `empty`/`cors` and the
// Accept asks for JSON. A page fetch elsewhere still uses `httpx.BrowserHeaders`.
func apiHeaders() map[string]string {
	return map[string]string{
		"User-Agent":      httpx.UserAgentFirefox,
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "en-US,en;q=0.5",
		"Sec-Fetch-Dest":  "empty",
		"Sec-Fetch-Mode":  "cors",
		"Sec-Fetch-Site":  "same-origin",
	}
}

// FetchPage retrieves a single page of the video listing API.
func (s *Scraper) FetchPage(ctx context.Context, page int) (VideosPage, error) {
	args, _ := json.Marshal([]any{[]string{fmt.Sprintf("page=%d", page)}})
	reqURL := fmt.Sprintf("%s%s?functionName=fetchVideosApi&args=%s", s.SiteBase, apiPath, args)

	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     reqURL,
		Headers: apiHeaders(),
	})
	if err != nil {
		return VideosPage{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return VideosPage{}, err
	}

	var outer apiResponse
	if err := json.Unmarshal(body, &outer); err != nil {
		return VideosPage{}, fmt.Errorf("decode API response: %w", err)
	}
	if !outer.OK {
		return VideosPage{}, fmt.Errorf("API returned ok=false: %s", string(outer.Data))
	}

	var vp VideosPage
	if err := json.Unmarshal(outer.Data, &vp); err != nil {
		return VideosPage{}, fmt.Errorf("decode videos page: %w", err)
	}
	return vp, nil
}

// BuildScene constructs a models.Scene from an API video entry, including
// detail page enrichment.
func (s *Scraper) BuildScene(ctx context.Context, studioURL string, vid APIVideo) (models.Scene, error) {
	now := time.Now().UTC()

	scene := models.Scene{
		ID:        strconv.Itoa(vid.ID),
		SiteID:    s.cfg.SiteID,
		StudioURL: studioURL,
		Title:     vid.Title,
		URL:       fmt.Sprintf("%s/%d-%s", s.SiteBase, vid.ContentMappingID, Slugify(vid.Title)),
		Thumbnail: vid.PosterSrc,
		Preview:   vid.SystemPreviewVideoFullSrc,
		Duration:  vid.Duration,
		Date:      ParseDate(vid.PublishDate),
		Studio:    s.cfg.StudioName,
		Views:     vid.ViewsCount,
		Likes:     vid.LikesCount,
		Comments:  vid.CommentsCount,
		ScrapedAt: now,
	}

	if vid.Has4K {
		scene.Width = 3840
		scene.Height = 2160
		scene.Resolution = "2160p"
	}

	if p, ok := vid.Price(); ok {
		scene.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: p,
			IsFree:  p == 0,
		})
	}

	detail, err := s.FetchDetail(ctx, scene.URL)
	if err == nil {
		if detail.Description != "" {
			scene.Description = detail.Description
		}
		if len(detail.Performers) > 0 {
			scene.Performers = detail.Performers
		}
		if len(detail.Tags) > 0 {
			scene.Tags = detail.Tags
		}
		if detail.Thumbnail != "" {
			scene.Thumbnail = detail.Thumbnail
		}
	}

	return scene, nil
}

// SceneDetail holds data extracted from a detail page.
type SceneDetail struct {
	Description string
	Performers  []string
	Tags        []string
	Thumbnail   string
}

var keywordsRe = regexp.MustCompile(`keywords\\":\\"([^\\]+)\\`)

// FetchDetail retrieves and parses a scene's detail page.
func (s *Scraper) FetchDetail(ctx context.Context, pageURL string) (SceneDetail, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return SceneDetail{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return SceneDetail{}, err
	}

	var d SceneDetail

	og := parseutil.OpenGraph(body)
	if v := og["og:description"]; v != "" {
		d.Description = html.UnescapeString(v)
	}
	if v := og["og:image"]; v != "" {
		d.Thumbnail = html.UnescapeString(v)
	}

	if m := keywordsRe.FindSubmatch(body); m != nil {
		d.Performers, d.Tags = SplitKeywords(string(m[1]), s.cfg.KnownPerformers)
	}

	return d, nil
}

// SplitKeywords separates a comma-delimited keyword string into performers
// and tags using the provided known-performers map.
func SplitKeywords(keywords string, known map[string]bool) (performers, tags []string) {
	for _, kw := range strings.Split(keywords, ", ") {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		if known[strings.ToLower(kw)] {
			performers = append(performers, kw)
		} else {
			tags = append(tags, kw)
		}
	}
	return performers, tags
}

// ParseDate parses the RFC3339-style timestamps from the API.
func ParseDate(s string) time.Time {
	t, _ := parseutil.TryParseDate(s, time.RFC3339Nano, "2006-01-02T15:04:05.000000Z")
	return t.UTC()
}

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify converts a title into a URL-safe slug.
func Slugify(title string) string {
	s := strings.ToLower(title)
	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
