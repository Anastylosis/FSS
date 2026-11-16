// Package beddableboys scrapes beddableboys.com's public tour, a Next.js site
// whose listing pages embed the scene data as __NEXT_DATA__ JSON. See
// docs/scrapers.md.
package beddableboys

import (
	"context"
	"encoding/json"
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

const (
	siteID     = "beddableboys"
	studioName = "Beddable Boys"
	listPath   = "/videos?page="
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?beddableboys\.com(?:/|$)`)

	nextDataRe = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__"[^>]*>(.*?)</script>`)
)

// Scraper implements scraper.StudioScraper for beddableboys.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Beddable Boys scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: "https://www.beddableboys.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"beddableboys.com", "beddableboys.com/videos?page={page}"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + listPath + strconv.Itoa(page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items, total, perPage, err := parsePage(body)
		if err != nil {
			return scraper.PageResult{}, scraper.ParseError(pageURL, err)
		}
		if page == 1 && len(items) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scenes on the first page"))
		}
		var scenes []models.Scene
		for _, it := range items {
			if seen[it.SceneCode] {
				continue
			}
			seen[it.SceneCode] = true
			scenes = append(scenes, it.toScene(s.base, studioURL, now))
		}
		return scraper.PageResult{Scenes: scenes, Total: total, Done: len(items) == 0 || len(items) < perPage}, nil
	})
}

type item struct {
	Title     string   `json:"title"`
	Slug      string   `json:"slug"`
	SceneCode string   `json:"scene_code"`
	Date      string   `json:"publish_date"`
	Desc      string   `json:"description"`
	Thumb     string   `json:"thumbnail"`
	Duration  string   `json:"videos_duration"`
	Views     int      `json:"views"`
	Likes     int      `json:"likes"`
	Tags      []string `json:"tags"`
	Models    []string `json:"models"`
}

func (it item) toScene(base, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          it.SceneCode,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       strings.TrimSpace(it.Title),
		URL:         base + "/videos/" + it.Slug + "?scene_code=" + it.SceneCode,
		Description: strings.TrimSpace(it.Desc),
		Performers:  it.Models,
		Tags:        it.Tags,
		Views:       it.Views,
		Likes:       it.Likes,
		Studio:      studioName,
		ScrapedAt:   now,
	}
	if strings.HasPrefix(it.Thumb, "//") {
		sc.Thumbnail = "https:" + it.Thumb
	} else {
		sc.Thumbnail = it.Thumb
	}
	if d, err := time.Parse("2006/01/02 15:04:05", it.Date); err == nil {
		sc.Date = d.UTC()
	}
	if f, err := strconv.ParseFloat(it.Duration, 64); err == nil {
		sc.Duration = int(f)
	}
	return sc
}

func parsePage(body string) (items []item, total, perPage int, err error) {
	m := nextDataRe.FindStringSubmatch(body)
	if m == nil {
		return nil, 0, 0, fmt.Errorf("no __NEXT_DATA__ block")
	}
	var nd struct {
		Props struct {
			PageProps struct {
				Contents struct {
					Data    []item `json:"data"`
					Total   int    `json:"total"`
					PerPage int    `json:"per_page"`
				} `json:"contents"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal([]byte(m[1]), &nd); err != nil {
		return nil, 0, 0, err
	}
	c := nd.Props.PageProps.Contents
	return c.Data, c.Total, c.PerPage, nil
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
