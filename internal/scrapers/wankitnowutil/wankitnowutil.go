package wankitnowutil

import (
	"context"
	"fmt"
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
	Domain   string
	Studio   string
	Patterns []string
	MatchRe  *regexp.Regexp
	BaseURL  string // optional override for testing; defaults to "https://www." + Domain
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

func (s *Scraper) ID() string { return s.cfg.ID }
func (s *Scraper) Patterns() []string {
	return append(s.cfg.Patterns, s.cfg.Domain+"/models/{slug}")
}
func (s *Scraper) MatchesURL(u string) bool {
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) siteBase() string {
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL
	}
	return "https://www." + s.cfg.Domain
}

var modelSlugRe = regexp.MustCompile(`/models/([^/?#]+)`)

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type nextDataResponse struct {
	PageProps struct {
		Contents struct {
			Total      int         `json:"total"`
			TotalPages int         `json:"total_pages"`
			Data       []sceneJSON `json:"data"`
		} `json:"contents"`
	} `json:"pageProps"`
}

type sceneJSON struct {
	ID              int              `json:"id"`
	Title           string           `json:"title"`
	Slug            string           `json:"slug"`
	Description     string           `json:"description"`
	PublishDate     string           `json:"publish_date"`
	SecondsDuration int              `json:"seconds_duration"`
	Thumb           string           `json:"thumb"`
	Models          []string         `json:"models"`
	Tags            []string         `json:"tags"`
	Site            string           `json:"site"`
	SiteDomain      string           `json:"site_domain"`
	Rating          float64          `json:"rating"`
	ModelsSlugs     []modelSlugEntry `json:"models_slugs"`
}

type modelSlugEntry struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

var buildIDRe = regexp.MustCompile(`"buildId"\s*:\s*"([^"]+)"`)

func (s *Scraper) fetchBuildID(ctx context.Context) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     s.siteBase() + "/",
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return "", fmt.Errorf("fetch homepage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read homepage: %w", err)
	}

	m := buildIDRe.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("buildId not found in homepage")
	}
	return string(m[1]), nil
}

func (s *Scraper) fetchPage(ctx context.Context, buildID string, page int) (*nextDataResponse, error) {
	u := fmt.Sprintf("%s/_next/data/%s/videos.json?page=%d", s.siteBase(), buildID, page)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var data nextDataResponse
	if err := httpx.DecodeJSON(resp.Body, &data); err != nil {
		return nil, fmt.Errorf("decode page %d: %w", page, err)
	}
	return &data, nil
}

type modelDataResponse struct {
	PageProps struct {
		ModelLatestContents []sceneJSON `json:"model_latest_contents"`
	} `json:"pageProps"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if m := modelSlugRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: detected model page: %s", s.cfg.ID, m[1])
		s.scrapeModelPage(ctx, m[1], studioURL, opts, out)
		return
	}

	scraper.Debugf(1, "%s: fetching build ID", s.cfg.ID)
	buildID, err := s.fetchBuildID(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	siteBase := s.siteBase()
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		data, err := s.fetchPage(ctx, buildID, page)
		if err != nil {
			return scraper.PageResult{}, err
		}

		contents := data.PageProps.Contents
		scenes := make([]models.Scene, 0, len(contents.Data))
		for _, sc := range contents.Data {
			scenes = append(scenes, toScene(sc, s.cfg.ID, siteBase, studioURL, s.cfg.Studio, now))
		}

		return scraper.PageResult{
			Scenes: scenes,
			Total:  contents.Total,
			Done:   page >= contents.TotalPages,
		}, nil
	})
}

func (s *Scraper) scrapeModelPage(ctx context.Context, slug, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	buildID, err := s.fetchBuildID(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	u := fmt.Sprintf("%s/_next/data/%s/models/%s.json", s.siteBase(), buildID, slug)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("model page %s: %w", slug, err)):
		case <-ctx.Done():
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()

	var data modelDataResponse
	if err := httpx.DecodeJSON(resp.Body, &data); err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("decode model page %s: %w", slug, err)):
		case <-ctx.Done():
		}
		return
	}

	scenes := data.PageProps.ModelLatestContents
	if len(scenes) == 0 {
		return
	}
	scraper.Debugf(1, "%s: found %d scenes for model %s", s.cfg.ID, len(scenes), slug)

	siteBase := s.siteBase()
	now := time.Now().UTC()

	select {
	case out <- scraper.Progress(len(scenes)):
	case <-ctx.Done():
		return
	}

	for _, sc := range scenes {
		id := strconv.Itoa(sc.ID)
		if opts.KnownIDs[id] {
			scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.ID, id)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(toScene(sc, s.cfg.ID, siteBase, studioURL, s.cfg.Studio, now)):
		case <-ctx.Done():
			return
		}
	}
}

func toScene(sc sceneJSON, siteID, siteBase, studioURL, studio string, now time.Time) models.Scene {
	var date time.Time
	if sc.PublishDate != "" {
		if t, err := time.Parse("2006/01/02 15:04:05", sc.PublishDate); err == nil {
			date = t.UTC()
		}
	}

	performers := make([]string, 0, len(sc.Models))
	for _, m := range sc.Models {
		name := strings.TrimSpace(m)
		if name != "" {
			performers = append(performers, normalizePerformer(name))
		}
	}

	return models.Scene{
		ID:          strconv.Itoa(sc.ID),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       sc.Title,
		Description: sc.Description,
		URL:         siteBase + "/videos/" + sc.Slug,
		Thumbnail:   sc.Thumb,
		Date:        date,
		Duration:    sc.SecondsDuration,
		Performers:  performers,
		Tags:        sc.Tags,
		Studio:      studio,
		ScrapedAt:   now,
	}
}

func normalizePerformer(name string) string {
	words := strings.Fields(name)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}
