package paysite

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	ID       string
	SiteBase string
	Patterns []string
	MatchRe  *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string         { return s.cfg.ID }
func (s *Scraper) Patterns() []string { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool {
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, s.cfg.SiteBase, opts, out)
	return out, nil
}

var nextDataRe = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__"[^>]*>(.*?)</script>`)

type nextData struct {
	Props struct {
		PageProps struct {
			Contents pageContents `json:"contents"`
		} `json:"pageProps"`
	} `json:"props"`
}

type pageContents struct {
	Total      int         `json:"total"`
	Page       json.Number `json:"page"`
	TotalPages int         `json:"total_pages"`
	Data       []scene     `json:"data"`
}

type scene struct {
	ID               int                    `json:"id"`
	Title            string                 `json:"title"`
	Slug             string                 `json:"slug"`
	Description      string                 `json:"description"`
	PublishDate      string                 `json:"publish_date"`
	SecondsDuration  int                    `json:"seconds_duration"`
	Models           []string               `json:"models"`
	Tags             []string               `json:"tags"`
	Thumb            string                 `json:"thumb"`
	TrailerScreencap string                 `json:"trailer_screencap"`
	Site             string                 `json:"site"`
	Videos           map[string]videoFormat `json:"videos"`
}

type videoFormat struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (s *Scraper) run(ctx context.Context, studioURL, base string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/scenes?page=%d", base, page)
		body, err := s.fetchPage(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}

		contents, err := parsePage(body)
		if err != nil {
			return scraper.PageResult{}, err
		}

		scenes := make([]models.Scene, len(contents.Data))
		for i, sc := range contents.Data {
			scenes[i] = toScene(s.cfg.ID, base, studioURL, sc)
		}
		return scraper.PageResult{
			Scenes: scenes,
			Total:  contents.Total,
			Done:   page >= contents.TotalPages,
		}, nil
	})
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func parsePage(body []byte) (*pageContents, error) {
	m := nextDataRe.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("__NEXT_DATA__ not found")
	}
	var nd nextData
	if err := json.Unmarshal(m[1], &nd); err != nil {
		return nil, fmt.Errorf("parse __NEXT_DATA__: %w", err)
	}
	return &nd.Props.PageProps.Contents, nil
}

func toScene(siteID, base, studioURL string, sc scene) models.Scene {
	id := strconv.Itoa(sc.ID)
	sceneURL := fmt.Sprintf("%s/scenes/%s", base, sc.Slug)

	var date time.Time
	if t, err := time.Parse("2006/01/02 15:04:05", sc.PublishDate); err == nil {
		date = t.UTC()
	}

	desc := html.UnescapeString(sc.Description)
	desc = strings.ReplaceAll(desc, " ", " ")
	desc = strings.TrimSpace(desc)

	thumb := sc.TrailerScreencap
	if thumb == "" {
		thumb = sc.Thumb
	}

	var width, height int
	for _, key := range []string{"orig", "hq", "stream", "mobile"} {
		if vf, ok := sc.Videos[key]; ok && vf.Width > width {
			width = vf.Width
			height = vf.Height
		}
	}

	var resolution string
	switch {
	case height >= 2160:
		resolution = "2160p"
	case height >= 1080:
		resolution = "1080p"
	case height >= 720:
		resolution = "720p"
	case height >= 480:
		resolution = "480p"
	}

	return models.Scene{
		ID:          id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       sc.Title,
		URL:         sceneURL,
		Date:        date,
		Description: desc,
		Duration:    sc.SecondsDuration,
		Performers:  sc.Models,
		Tags:        sc.Tags,
		Thumbnail:   thumb,
		Studio:      strings.TrimSpace(sc.Site),
		Width:       width,
		Height:      height,
		Resolution:  resolution,
		ScrapedAt:   time.Now().UTC(),
	}
}
