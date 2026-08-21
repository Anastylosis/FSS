package yourvids

import (
	"context"
	"encoding/json"
	"errors"
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

type Scraper struct {
	client  *http.Client
	apiBase string
}

func New() *Scraper {
	return &Scraper{
		client:  httpx.NewClient(30 * time.Second),
		apiBase: "https://yourvids.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "yourvids" }

func (s *Scraper) Patterns() []string {
	return []string{"yourvids.com/creators/{slug}", "yourvids.com/{slug}"}
}

var (
	creatorRe = regexp.MustCompile(`^https?://(?:www\.)?yourvids\.com/creators/[\w-]+`)
	aliasRe   = regexp.MustCompile(`^https?://(?:www\.)?yourvids\.com/([\w-]+)/?$`)
	// Deliberately not host-anchored: offline tests point the scraper at a
	// local server, and MatchesURL has already vouched for the host.
	creatorPathRe = regexp.MustCompile(`/creators/([\w-]+)`)
)

// reservedPaths are the site's own top-level pages. Everything else in that
// namespace is an account slug — but creators and ordinary viewers share it,
// and only the API can tell them apart, so a bare-slug URL is accepted here and
// rejected at the first request if it names no creator.
var reservedPaths = map[string]bool{
	"api":          true,
	"boutique":     true,
	"community":    true,
	"contests":     true,
	"creators":     true,
	"help":         true,
	"leaderboards": true,
	"login":        true,
	"register":     true,
	"search":       true,
	"vids":         true,
	"yourvoices":   true,
}

func (s *Scraper) MatchesURL(u string) bool {
	return creatorRe.MatchString(u) || aliasSlug(u) != ""
}

// aliasSlug returns the creator slug of a bare vanity URL, or "" if the path is
// one of the site's own pages.
func aliasSlug(u string) string {
	m := aliasRe.FindStringSubmatch(u)
	if m == nil || reservedPaths[strings.ToLower(m[1])] {
		return ""
	}
	return m[1]
}

// creatorSlug extracts the creator slug from either the canonical
// /creators/{slug} URL or the bare /{slug} vanity alias the site also serves.
func creatorSlug(u string) string {
	if m := creatorPathRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return aliasSlug(u)
}

// PreferredStudioURL maps the vanity alias onto the canonical creator URL so
// scraping a creator both ways stores one studio rather than two.
func (s *Scraper) PreferredStudioURL(studioURL string) string {
	if !s.MatchesURL(studioURL) {
		return ""
	}
	slug := creatorSlug(studioURL)
	if slug == "" {
		return ""
	}
	return "https://yourvids.com/creators/" + slug
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	slug := creatorSlug(studioURL)
	if slug == "" {
		return nil, fmt.Errorf("cannot extract creator slug from %q", studioURL)
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, slug, opts, out)
	return out, nil
}

// ---- API types ----

type apiResponse struct {
	Success bool    `json:"success"`
	Data    apiData `json:"data"`
}

type apiData struct {
	Videos     []apiVideo    `json:"videos"`
	Pagination apiPagination `json:"pagination"`
}

type apiPagination struct {
	CurrentPage int  `json:"current_page"`
	PerPage     int  `json:"per_page"`
	Total       int  `json:"total"`
	TotalPages  int  `json:"total_pages"`
	HasMore     bool `json:"has_more"`
}

type apiVideo struct {
	ID            int             `json:"id"`
	Title         string          `json:"title"`
	Thumbnail     string          `json:"thumbnail"`
	PreviewURL    string          `json:"preview_url"`
	CreatorName   string          `json:"creator_name"`
	Duration      string          `json:"duration"`
	Views         int             `json:"views"`
	Likes         int             `json:"likes"`
	Price         string          `json:"price"`
	OriginalPrice json.RawMessage `json:"original_price"`
	VideoURL      string          `json:"video_url"`
	CreatorURL    string          `json:"creator_url"`
	IsOnSale      bool            `json:"is_on_sale"`
	IsFree        bool            `json:"is_free"`
	IsHD          bool            `json:"is_hd"`
	Is4K          bool            `json:"is_4k"`
	IsAudio       bool            `json:"is_audio"`
	CreatedAt     string          `json:"created_at"`
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL, slug string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = 3
	}

	var allVideos []apiVideo
	stoppedEarly := false

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return
			}
		}
		scraper.Debugf(1, "yourvids: fetching page %d", page)

		apiURL := fmt.Sprintf("%s/api/creators/%s/videos?page=%d&sort=newest", s.apiBase, slug, page)
		resp, err := s.fetchAPI(ctx, apiURL)
		if err != nil {
			if page == 1 {
				err = notACreator(slug, err)
			}
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		if page == 1 && resp.Data.Pagination.Total > 0 {
			select {
			case out <- scraper.Progress(resp.Data.Pagination.Total):
			case <-ctx.Done():
				return
			}
		}

		hitKnown := false
		for _, v := range resp.Data.Videos {
			id := strconv.Itoa(v.ID)
			if opts.KnownIDs[id] {
				hitKnown = true
				stoppedEarly = true
				break
			}
			allVideos = append(allVideos, v)
		}

		if hitKnown || !resp.Data.Pagination.HasMore {
			break
		}
	}

	type detailResult struct {
		video       apiVideo
		description string
		tags        []string
	}

	work := make(chan apiVideo, workers)
	results := make(chan detailResult, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for v := range work {
				if ctx.Err() != nil {
					return
				}
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				desc, tags := s.fetchDetail(ctx, v.VideoURL)
				select {
				case results <- detailResult{video: v, description: desc, tags: tags}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		for _, v := range allVideos {
			select {
			case work <- v:
			case <-ctx.Done():
			}
			if ctx.Err() != nil {
				break
			}
		}
		close(work)
		wg.Wait()
		close(results)
	}()

	now := time.Now().UTC()
	for dr := range results {
		scene := toScene(studioURL, dr.video, dr.description, dr.tags, now)
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}

	if stoppedEarly {
		scraper.Debugf(1, "yourvids: hit known ID, stopping early")
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
}

// notACreator turns the 404 a viewer account produces into an error that says
// so. Creators and ordinary accounts share the top-level path namespace, so a
// bare-slug URL cannot be told apart from a creator's until this first request
// comes back. Deliberately not a scraper.AbsentError: the whole catalogue is
// missing, not an optional sub-listing, so the run must count as incomplete.
func notACreator(slug string, err error) error {
	var se *httpx.StatusError
	if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
		return fmt.Errorf("no creator %q on yourvids (the bare /%s URL may be a viewer account, which has no catalogue): %w", slug, slug, err)
	}
	return err
}

// ---- API fetch ----

func (s *Scraper) fetchAPI(ctx context.Context, apiURL string) (*apiResponse, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: apiURL,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Accept"] = "application/json"
			h["X-Requested-With"] = "XMLHttpRequest"
			return h
		}(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var ar apiResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	if !ar.Success {
		return nil, fmt.Errorf("API returned success=false")
	}
	return &ar, nil
}

// ---- detail page ----

var (
	dataTagRe   = regexp.MustCompile(`data-tag="([\w-]+)"`)
	descBlockRe = regexp.MustCompile(`(?s)<div[^>]*class="rich-text-content[^"]*"[^>]*>\s*(.*?)\s*</div>`)
)

func (s *Scraper) fetchDetail(ctx context.Context, videoURL string) (description string, tags []string) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: videoURL,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Accept"] = "text/html"
			return h
		}(),
	})
	if err != nil {
		return "", nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", nil
	}

	tagMatches := dataTagRe.FindAllSubmatch(body, -1)
	seen := make(map[string]bool)
	for _, m := range tagMatches {
		tag := titleCase(strings.ReplaceAll(string(m[1]), "-", " "))
		if !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}

	descMatches := descBlockRe.FindAllSubmatch(body, -1)
	var longest string
	for _, m := range descMatches {
		raw := string(m[1])
		if len(raw) > len(longest) {
			longest = raw
		}
	}
	if longest != "" {
		description = cleanHTML(longest)
	}

	return description, tags
}

// ---- conversion ----

func toScene(studioURL string, v apiVideo, description string, tags []string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:          strconv.Itoa(v.ID),
		SiteID:      "yourvids",
		StudioURL:   studioURL,
		Title:       v.Title,
		URL:         v.VideoURL,
		Thumbnail:   v.Thumbnail,
		Preview:     v.PreviewURL,
		Duration:    parseutil.ParseDurationColon(v.Duration),
		Views:       v.Views,
		Likes:       v.Likes,
		Performers:  []string{v.CreatorName},
		Studio:      v.CreatorName,
		Description: description,
		Tags:        tags,
		ScrapedAt:   now,
	}

	if v.Is4K {
		scene.Width = 3840
		scene.Height = 2160
		scene.Resolution = "4K"
	} else if v.IsHD {
		scene.Width = 1920
		scene.Height = 1080
		scene.Resolution = "1080p"
	}

	if t, err := time.Parse("2006-01-02 15:04:05", v.CreatedAt); err == nil {
		scene.Date = t.UTC()
	}

	price, priceErr := strconv.ParseFloat(v.Price, 64)
	if v.IsFree || priceErr == nil {
		snap := models.PriceSnapshot{
			Date:   now,
			IsFree: v.IsFree,
		}
		if v.IsOnSale && len(v.OriginalPrice) > 0 && string(v.OriginalPrice) != "null" {
			orig := parseOriginalPrice(v.OriginalPrice)
			snap.Regular = orig
			snap.Discounted = price
			snap.IsOnSale = true
			if orig > 0 {
				snap.DiscountPercent = int((1 - price/orig) * 100)
			}
		} else {
			snap.Regular = price
		}
		scene.AddPrice(snap)
	}

	return scene
}

// ---- helpers ----

func parseOriginalPrice(raw json.RawMessage) float64 {
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		f, _ = strconv.ParseFloat(s, 64)
		return f
	}
	return 0
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

func cleanHTML(s string) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = strings.ReplaceAll(s, "</p>", "\n")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	var trimmed []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			trimmed = append(trimmed, l)
		}
	}
	return strings.Join(trimmed, "\n")
}
