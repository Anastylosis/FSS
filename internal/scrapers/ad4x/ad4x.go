package ad4x

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const defaultBase = "https://ad4x.com"

type Scraper struct {
	Client  *http.Client
	baseURL string
}

func New() *Scraper {
	return &Scraper{
		Client:  httpx.NewClient(30 * time.Second),
		baseURL: defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "ad4x" }

func (s *Scraper) Patterns() []string {
	return []string{
		"ad4x.com",
		"ad4x.com/en/videos",
		"ad4x.com/en/models/{slug}",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?ad4x\.com(?:/|$)`)
	modelRe = regexp.MustCompile(`/(?:en|fr)/models/([\w-]+)`)
	nextRe  = regexp.MustCompile(`(?s)<script\s+id="__NEXT_DATA__"\s+type="application/json">(.*?)</script>`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// --- Next.js data types ---

type nextData struct {
	Props struct {
		PageProps struct {
			Contents      contentResponse `json:"contents"`
			Content       *contentItem    `json:"content"`
			Model         *modelData      `json:"model"`
			ModelContents []contentItem   `json:"model_contents"`
		} `json:"pageProps"`
	} `json:"props"`
}

type contentResponse struct {
	Total      int           `json:"total"`
	Page       string        `json:"page"`
	TotalPages int           `json:"total_pages"`
	Data       []contentItem `json:"data"`
}

type contentItem struct {
	ID              int         `json:"id"`
	Title           string      `json:"title"`
	Slug            string      `json:"slug"`
	PublishDate     string      `json:"publish_date"`
	SecondsDuration int         `json:"seconds_duration"`
	Thumb           string      `json:"thumb"`
	Models          []string    `json:"models"`
	ModelsSlugs     []modelSlug `json:"models_slugs"`
	Tags            []string    `json:"tags"`
	Description     string      `json:"description"`
	ContentPrice    float64     `json:"content_price"`
}

type modelSlug struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type modelData struct {
	Contents contentResponse `json:"contents"`
}

// --- runner ---

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()

	if m := modelRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "ad4x: scraping model page %s", m[1])
		s.scrapeModelPage(ctx, studioURL, opts, out, now)
		return
	}

	scraper.Paginate(ctx, opts, "ad4x", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/en/videos?page=%d", s.baseURL, page)
		items, total, totalPages, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = toScene(item, studioURL, now)
		}
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   len(items) == 0 || (totalPages > 0 && page >= totalPages),
		}, nil
	})
}

func (s *Scraper) scrapeModelPage(ctx context.Context, studioURL string, _ scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	body, err := s.fetchPage(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("model page: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	nd, err := parseNextData(body)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("model page: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	var items []contentItem
	if len(nd.Props.PageProps.ModelContents) > 0 {
		items = nd.Props.PageProps.ModelContents
	} else if nd.Props.PageProps.Model != nil {
		items = nd.Props.PageProps.Model.Contents.Data
	}

	scraper.Debugf(1, "ad4x: model page has %d scenes", len(items))
	if len(items) > 0 {
		select {
		case out <- scraper.Progress(len(items)):
		case <-ctx.Done():
			return
		}
	}

	for _, item := range items {
		scene := toScene(item, studioURL, now)
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]contentItem, int, int, error) {
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return nil, 0, 0, err
	}

	nd, err := parseNextData(body)
	if err != nil {
		return nil, 0, 0, err
	}

	c := nd.Props.PageProps.Contents
	return c.Data, c.Total, c.TotalPages, nil
}

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func parseNextData(body []byte) (nextData, error) {
	m := nextRe.FindSubmatch(body)
	if m == nil {
		return nextData{}, fmt.Errorf("__NEXT_DATA__ not found")
	}
	var nd nextData
	if err := json.Unmarshal(m[1], &nd); err != nil {
		return nextData{}, fmt.Errorf("parsing __NEXT_DATA__: %w", err)
	}
	return nd, nil
}

func toScene(item contentItem, studioURL string, now time.Time) models.Scene {
	var date time.Time
	if item.PublishDate != "" {
		// A date-only value and a T-separated one both occur.
		date, _ = parseutil.TryParseDate(item.PublishDate,
			"2006/01/02 15:04:05", "2006-01-02 15:04:05", "2006/01/02", time.RFC3339)
		date = date.UTC()
	}

	var performers []string
	for _, ms := range item.ModelsSlugs {
		if ms.Name != "" {
			performers = append(performers, ms.Name)
		}
	}
	if len(performers) == 0 {
		performers = item.Models
	}

	scene := models.Scene{
		ID:          strconv.Itoa(item.ID),
		SiteID:      "ad4x",
		StudioURL:   studioURL,
		Title:       item.Title,
		URL:         defaultBase + "/en/videos/" + item.Slug,
		Thumbnail:   item.Thumb,
		Duration:    item.SecondsDuration,
		Date:        date,
		Description: item.Description,
		Tags:        item.Tags,
		Performers:  performers,
		Studio:      "AD4X",
		ScrapedAt:   now,
	}
	if item.ContentPrice > 0 {
		scene.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: item.ContentPrice,
		})
	}
	return scene
}
