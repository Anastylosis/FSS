package pornbox

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const defaultBase = "https://pornbox.com"

type Scraper struct {
	client  *http.Client
	baseURL string

	sessMu sync.Mutex
	sessOK bool
}

func New() *Scraper {
	jar, _ := cookiejar.New(nil)
	c := httpx.NewClient(30 * time.Second)
	c.Jar = jar
	return &Scraper{
		client:  c,
		baseURL: defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "pornbox" }

func (s *Scraper) Patterns() []string {
	return []string{
		"pornbox.com/application/studio/{id}",
		"pornbox.com/application/model/{id}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.|teen\.)?pornbox\.com/application/(?:studio|model)/\d+`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type urlMode int

const (
	modeStudio urlMode = iota
	modeModel
)

var (
	studioURLRe = regexp.MustCompile(`/application/studio/(\d+)`)
	modelURLRe  = regexp.MustCompile(`/application/model/(\d+)`)
)

func classifyURL(u string) (urlMode, string) {
	if m := studioURLRe.FindStringSubmatch(u); m != nil {
		return modeStudio, m[1]
	}
	if m := modelURLRe.FindStringSubmatch(u); m != nil {
		return modeModel, m[1]
	}
	return modeStudio, ""
}

func (s *Scraper) listingURL(mode urlMode, id string, page int) string {
	switch mode {
	case modeModel:
		return fmt.Sprintf("%s/model/content/%s/?skip=%d&sort=latest", s.baseURL, id, page)
	default:
		return fmt.Sprintf("%s/studio/%s/?skip=%d&sort=latest", s.baseURL, id, page)
	}
}

func (s *Scraper) studioInfoURL(id string) string {
	return fmt.Sprintf("%s/studio/info/%s", s.baseURL, id)
}

type studioInfoResp struct {
	Name string `json:"name"`
}

type listingResp struct {
	Contents    []contentItem `json:"contents"`
	TotalCount  int           `json:"totalCount"`
	TotalPages  int           `json:"totalPages"`
	CurrentPage int           `json:"currentPage"`
}

type contentItem struct {
	ID          int          `json:"content_id"`
	SceneName   string       `json:"scene_name"`
	PublishDate string       `json:"publish_date"`
	Runtime     string       `json:"runtime"`
	Studio      string       `json:"studio"`
	Models      []modelRef   `json:"models"`
	Niches      []nicheRef   `json:"niches"`
	Thumbnail   thumbnailSet `json:"thumbnail"`
	PriceUSD    float64      `json:"content_price_usd"`
}

type modelRef struct {
	ModelName string `json:"model_name"`
	ModelID   int    `json:"model_id"`
}

type nicheRef struct {
	Niche string `json:"niche"`
}

type thumbnailSet struct {
	Large string `json:"large"`
	List  string `json:"list"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	mode, id := classifyURL(studioURL)
	if id == "" {
		select {
		case out <- scraper.Error(fmt.Errorf("cannot parse studio/model ID from URL: %s", studioURL)):
		case <-ctx.Done():
		}
		return
	}

	studioName := ""
	if mode == modeStudio {
		if name, err := s.fetchStudioName(ctx, id); err == nil {
			studioName = name
		}
	}

	for page := 0; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 0 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return
			}
		}
		scraper.Debugf(1, "pornbox: fetching page %d", page)

		u := s.listingURL(mode, id, page)
		listing, err := s.fetchListing(ctx, u)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		if page == 0 {
			select {
			case out <- scraper.Progress(listing.TotalCount):
			case <-ctx.Done():
				return
			}
		}

		if len(listing.Contents) == 0 {
			return
		}

		for _, item := range listing.Contents {
			sceneID := strconv.Itoa(item.ID)

			if opts.KnownIDs[sceneID] {
				scraper.Debugf(1, "pornbox: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}

			scene := toScene(item, studioURL, studioName)

			select {
			case out <- scraper.Scene(scene):
			case <-ctx.Done():
				return
			}
		}

		if page >= listing.TotalPages-1 {
			return
		}
	}
}

func toScene(item contentItem, studioURL, studioName string) models.Scene {
	sceneID := strconv.Itoa(item.ID)

	studio := studioName
	if studio == "" {
		studio = item.Studio
	}

	var performers []string
	for _, m := range item.Models {
		name := strings.TrimSpace(m.ModelName)
		if name != "" {
			performers = append(performers, name)
		}
	}

	var tags []string
	for _, n := range item.Niches {
		tag := strings.TrimSpace(n.Niche)
		if tag != "" {
			tags = append(tags, tag)
		}
	}

	var date time.Time
	if item.PublishDate != "" {
		if t, err := time.Parse(time.RFC3339Nano, item.PublishDate); err == nil {
			date = t.UTC()
		} else if t, err := time.Parse("2006-01-02T15:04:05.000Z", item.PublishDate); err == nil {
			date = t.UTC()
		}
	}

	dur := parseRuntime(item.Runtime)

	thumb := item.Thumbnail.Large
	if thumb == "" {
		thumb = item.Thumbnail.List
	}

	sc := models.Scene{
		ID:         sceneID,
		SiteID:     "pornbox",
		StudioURL:  studioURL,
		Title:      item.SceneName,
		URL:        fmt.Sprintf("https://pornbox.com/application/watch/%d", item.ID),
		Date:       date,
		Duration:   dur,
		Performers: performers,
		Tags:       tags,
		Thumbnail:  thumb,
		Studio:     studio,
		ScrapedAt:  time.Now().UTC(),
	}

	if item.PriceUSD > 0 {
		sc.AddPrice(models.PriceSnapshot{
			Date:    time.Now().UTC(),
			Regular: item.PriceUSD,
		})
	}

	return sc
}

func parseRuntime(s string) int {
	parts := strings.Split(strings.TrimSpace(s), ":")
	switch len(parts) {
	case 3:
		h, _ := strconv.Atoi(parts[0])
		m, _ := strconv.Atoi(parts[1])
		sec, _ := strconv.Atoi(parts[2])
		return h*3600 + m*60 + sec
	case 2:
		m, _ := strconv.Atoi(parts[0])
		sec, _ := strconv.Atoi(parts[1])
		return m*60 + sec
	}
	return 0
}

// initSession fetches the homepage once to seed the cookie jar. A failure is
// not cached — the bootstrap is a plain page fetch, so a transient one would
// otherwise poison every later request for the process lifetime.
func (s *Scraper) initSession(ctx context.Context) error {
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	if s.sessOK {
		return nil
	}
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     s.baseURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return fmt.Errorf("session bootstrap: %w", err)
	}
	_ = resp.Body.Close()
	s.sessOK = true
	return nil
}

func (s *Scraper) fetchJSON(ctx context.Context, url string, v any) error {
	if err := s.initSession(ctx); err != nil {
		return err
	}
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	h["X-Requested-With"] = "XMLHttpRequest"
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: h,
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.DecodeJSON(resp.Body, v)
}

func (s *Scraper) fetchListing(ctx context.Context, url string) (listingResp, error) {
	var listing listingResp
	if err := s.fetchJSON(ctx, url, &listing); err != nil {
		return listingResp{}, err
	}
	return listing, nil
}

func (s *Scraper) fetchStudioName(ctx context.Context, studioID string) (string, error) {
	u := s.studioInfoURL(studioID)
	var info studioInfoResp
	if err := s.fetchJSON(ctx, u, &info); err != nil {
		return "", err
	}
	return info.Name, nil
}
