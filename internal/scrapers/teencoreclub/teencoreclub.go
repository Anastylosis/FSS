package teencoreclub

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func init() { scraper.Register(New()) }

var apiBase = "https://api.fundorado.com"

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return "teencoreclub" }

func (s *Scraper) Patterns() []string {
	return []string{
		"teencoreclub.com",
		"teencoreclub.com/video/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?teencoreclub\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type siteConfig struct {
	ID      int            `json:"id"`
	Studios []configStudio `json:"studios"`
}

type configStudio struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type browseResponse struct {
	Videos struct {
		CurrentPage int         `json:"current_page"`
		Data        []videoItem `json:"data"`
		Total       int         `json:"total"`
		LastPage    int         `json:"last_page"`
	} `json:"videos"`
	SceneCount int `json:"scene_count"`
}

type videoItem struct {
	ID              int         `json:"id"`
	Title           langString  `json:"title"`
	Slug            string      `json:"slug"`
	Artwork         artworkObj  `json:"artwork"`
	PublicationDate string      `json:"publication_date"`
	Actors          []actorItem `json:"actors"`
	Meta            videoMeta   `json:"meta"`
	Views           int         `json:"views_total"`
}

type langString map[string]string

func (ls langString) String() string {
	if v, ok := ls["en"]; ok && v != "" {
		return v
	}
	for _, v := range ls {
		if v != "" {
			return v
		}
	}
	return ""
}

type artworkObj struct {
	Original string `json:"original"`
	Large    string `json:"large"`
	Medium   string `json:"medium"`
	Small    string `json:"small"`
}

type actorItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type videoMeta struct {
	DurationSeconds int    `json:"duration_seconds"`
	Year            int    `json:"year"`
	Aspect          string `json:"aspect"`
}

type videoDetail struct {
	ID          int           `json:"id"`
	Title       langString    `json:"title"`
	Slug        string        `json:"slug"`
	Description string        `json:"description"`
	Actors      []actorItem   `json:"actors"`
	Genres      []genreItem   `json:"genres"`
	Labels      []labelItem   `json:"labels"`
	Studio      *detailStudio `json:"studio"`
	Meta        videoMeta     `json:"meta"`
	Artwork     artworkObj    `json:"artwork"`
	Screenshots []string      `json:"screenshots"`
	Views       int           `json:"views_total"`
}

type genreItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type labelItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type detailStudio struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	cfg, err := s.fetchSiteConfig(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("fetching site config: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	studios := cfg.Studios
	if len(studios) == 0 {
		select {
		case out <- scraper.Error(fmt.Errorf("no studios found in site config")):
		case <-ctx.Done():
		}
		return
	}

	scraper.Debugf(1, "teencoreclub: found %d studios", len(studios))

	grandTotal := 0
	type studioTotal struct {
		studio configStudio
		total  int
	}
	var studioTotals []studioTotal

	for _, st := range studios {
		if ctx.Err() != nil {
			return
		}
		t, err := s.fetchStudioTotal(ctx, cfg.ID, st.ID)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("fetching total for %s: %w", st.Name, err)):
			case <-ctx.Done():
			}
			continue
		}
		if t > 0 {
			grandTotal += t
			studioTotals = append(studioTotals, studioTotal{studio: st, total: t})
		}
	}

	if grandTotal > 0 {
		scraper.Debugf(1, "teencoreclub: %d total scenes across %d studios", grandTotal, len(studioTotals))
		select {
		case out <- scraper.Progress(grandTotal):
		case <-ctx.Done():
			return
		}
	}

	for _, st := range studioTotals {
		if ctx.Err() != nil {
			return
		}
		s.scrapeStudio(ctx, cfg.ID, st.studio, st.total, studioURL, opts, out)
	}
}

func (s *Scraper) scrapeStudio(ctx context.Context, cfgSiteID int, studio configStudio, total int, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	scraper.Debugf(1, "teencoreclub: scraping studio %q (%d scenes)", studio.Name, total)

	lastPage := (total + 49) / 50

	scraper.Paginate(ctx, opts, "teencoreclub", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		resp, err := s.fetchBrowsePage(ctx, cfgSiteID, studio.ID, page)
		if err != nil {
			return scraper.PageResult{}, fmt.Errorf("studio %s: %w", studio.Name, err)
		}

		if len(resp.Videos.Data) == 0 {
			return scraper.PageResult{}, nil
		}

		scenes, err := s.enrichAndCollect(ctx, resp.Videos.Data, studio, studioURL, opts)
		if err != nil {
			return scraper.PageResult{}, err
		}
		return scraper.PageResult{Scenes: scenes, Done: page >= lastPage}, nil
	})
}

func (s *Scraper) enrichAndCollect(ctx context.Context, items []videoItem, studio configStudio, studioURL string, opts scraper.ListOpts) ([]models.Scene, error) {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	type enriched struct {
		item   videoItem
		detail *videoDetail
	}

	results := make([]enriched, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(idx int, item videoItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if !scraper.Pace(ctx, opts.Delay) {
				return
			}

			detail, err := s.fetchVideoDetail(ctx, item.ID)
			if err != nil {
				scraper.Debugf(2, "teencoreclub: detail fetch failed for %d: %v", item.ID, err)
				results[idx] = enriched{item: item}
				return
			}
			results[idx] = enriched{item: item, detail: detail}
		}(i, it)
	}
	wg.Wait()

	now := time.Now().UTC()
	var scenes []models.Scene
	for _, r := range results {
		if ctx.Err() != nil {
			break
		}
		scenes = append(scenes, toScene(r.item, r.detail, studio, studioURL, now))
	}
	return scenes, nil
}

func toScene(item videoItem, detail *videoDetail, studio configStudio, studioURL string, now time.Time) models.Scene {
	id := strconv.Itoa(item.ID)
	siteID := slugify(studio.Name)

	title := item.Title.String()
	description := ""
	var tags []string
	duration := item.Meta.DurationSeconds
	var performers []string
	thumbnail := item.Artwork.Original
	if thumbnail == "" {
		thumbnail = item.Artwork.Large
	}
	views := item.Views
	studioName := studio.Name

	for _, a := range item.Actors {
		performers = append(performers, a.Name)
	}

	if detail != nil {
		if detail.Description != "" {
			description = detail.Description
		}
		for _, g := range detail.Genres {
			tags = append(tags, g.Name)
		}
		if detail.Studio != nil && detail.Studio.Name != "" {
			studioName = detail.Studio.Name
		}
		if len(detail.Actors) > 0 && len(performers) == 0 {
			for _, a := range detail.Actors {
				performers = append(performers, a.Name)
			}
		}
		if len(detail.Labels) > 0 {
			label := strings.TrimSuffix(detail.Labels[0].Name, ".com")
			siteID = slugify(label)
		}
	}

	sceneURL := "https://teencoreclub.com/video/" + item.Slug

	var date time.Time
	if item.PublicationDate != "" {
		if t, err := time.Parse("2006-01-02", item.PublicationDate); err == nil {
			date = t
		} else if t, err := time.Parse("2006-01-02T15:04:05.000000Z", item.PublicationDate); err == nil {
			date = t
		}
	}

	return models.Scene{
		ID:          id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       title,
		URL:         sceneURL,
		Thumbnail:   thumbnail,
		Date:        date,
		Duration:    duration,
		Performers:  performers,
		Description: description,
		Tags:        tags,
		Studio:      studioName,
		Views:       views,
		ScrapedAt:   now,
	}
}

func slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, s)
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

func (s *Scraper) fetchSiteConfig(ctx context.Context) (*siteConfig, error) {
	var cfg siteConfig
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: apiBase + "/api/sitecfg?h=teencoreclub.com",
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
			"Referer":    "https://teencoreclub.com/",
			"Origin":     "https://teencoreclub.com",
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := httpx.DecodeJSON(resp.Body, &cfg); err != nil {
		return nil, fmt.Errorf("decoding site config: %w", err)
	}
	return &cfg, nil
}

func (s *Scraper) fetchStudioTotal(ctx context.Context, siteID, studioID int) (int, error) {
	u := fmt.Sprintf("%s/api/videos/browse/studio/%d?site_id=%d&page=1&lang=en&sg=0&video_type=scene",
		apiBase, studioID, siteID)

	var resp browseResponse
	httpResp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: u,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
			"Referer":    "https://teencoreclub.com/",
			"Origin":     "https://teencoreclub.com",
		},
	})
	if err != nil {
		return 0, err
	}
	defer func() { _ = httpResp.Body.Close() }()
	if err := httpx.DecodeJSON(httpResp.Body, &resp); err != nil {
		return 0, err
	}
	return resp.Videos.Total, nil
}

func (s *Scraper) fetchBrowsePage(ctx context.Context, siteID, studioID, page int) (*browseResponse, error) {
	u := fmt.Sprintf("%s/api/videos/browse/studio/%d?site_id=%d&page=%d&lang=en&sg=0&video_type=scene",
		apiBase, studioID, siteID, page)

	var resp browseResponse
	httpResp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: u,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
			"Referer":    "https://teencoreclub.com/",
			"Origin":     "https://teencoreclub.com",
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = httpResp.Body.Close() }()
	if err := httpx.DecodeJSON(httpResp.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *Scraper) fetchVideoDetail(ctx context.Context, videoID int) (*videoDetail, error) {
	u := fmt.Sprintf("%s/api/videodetail/%d", apiBase, videoID)

	var detail videoDetail
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: u,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
			"Referer":    "https://teencoreclub.com/",
			"Origin":     "https://teencoreclub.com",
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := httpx.DecodeJSON(resp.Body, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}
