package whutil

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	APIBase    string
	DetailPath string // scene URL path prefix, e.g. "/set/detail/" or "/detail/"
}

type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{cfg: cfg, Client: httpx.NewClient(30 * time.Second)}
}

type listResponse struct {
	Latest [][]listItem `json:"latest"`
	Count  string       `json:"count"`
	Pages  int          `json:"pages"`
}

type listItem struct {
	ID       int    `json:"id"`
	SetID    string `json:"setid"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Date     string `json:"date"`
	Image    string `json:"image"`
	CSRibbon int    `json:"cs_ribbon"`
}

type detailResponse struct {
	VideoDurationMin int    `json:"videoduration_min"`
	VideoDurationSec int    `json:"videoduration_sec"`
	Info             string `json:"info"`
	MainImage        string `json:"main_image"`
}

func (s *Scraper) Run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if opts.Workers <= 0 {
		opts.Workers = 3
	}

	work := make(chan listItem, opts.Workers)
	var wg sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene := s.toScene(item, studioURL)
				s.enrichDetail(ctx, &scene, item.SetID)
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	sentTotal := false
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			break
		}

		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)

		u := fmt.Sprintf("%sset/list?page=%d", s.cfg.APIBase, page)
		var lr listResponse
		if err := s.fetchJSON(ctx, u, &lr); err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		items := flattenItems(lr.Latest)
		if len(items) == 0 {
			break
		}

		if !sentTotal {
			sentTotal = true
			total := ParseCount(lr.Count)
			if total > 0 {
				scraper.Debugf(1, "%s: %d total scenes", s.cfg.SiteID, total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
				}
			}
		}

		hitKnown := false
		for _, item := range items {
			if item.CSRibbon == 1 {
				continue
			}
			if opts.KnownIDs[item.SetID] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.SiteID, item.SetID)
				hitKnown = true
				break
			}
			select {
			case work <- item:
			case <-ctx.Done():
				hitKnown = true
			}
			if ctx.Err() != nil {
				break
			}
		}
		if hitKnown {
			if opts.KnownIDs != nil {
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
			}
			break
		}

		if lr.Pages > 0 && page >= lr.Pages {
			break
		}
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) toScene(item listItem, studioURL string) models.Scene {
	now := time.Now().UTC()
	scene := models.Scene{
		ID:        item.SetID,
		SiteID:    s.cfg.SiteID,
		StudioURL: studioURL,
		Title:     item.Title,
		URL:       fmt.Sprintf("https://www.%s%s%s", s.cfg.Domain, s.cfg.DetailPath, item.SetID),
		Thumbnail: s.absURL(item.Image),
		Studio:    s.cfg.StudioName,
		ScrapedAt: now,
	}
	if item.Category != "" {
		scene.Tags = []string{item.Category}
	}
	if d, ok := ParseDate(item.Date); ok {
		scene.Date = d
	}
	return scene
}

func (s *Scraper) enrichDetail(ctx context.Context, scene *models.Scene, setID string) {
	u := s.cfg.APIBase + "set/data?setid=" + setID
	var dr detailResponse
	if err := s.fetchJSON(ctx, u, &dr); err != nil {
		return
	}
	dur := dr.VideoDurationMin*60 + dr.VideoDurationSec
	if dur > 0 {
		scene.Duration = dur
	}
	if dr.Info != "" {
		scene.Description = strings.TrimSpace(dr.Info)
	}
	if dr.MainImage != "" {
		scene.Thumbnail = dr.MainImage
	}
}

func (s *Scraper) absURL(path string) string {
	if strings.HasPrefix(path, "http") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "https://www." + s.cfg.Domain + path
}

func (s *Scraper) fetchJSON(ctx context.Context, rawURL string, v any) error {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL: rawURL,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
			"Cookie":     "adult=1",
		},
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.DecodeJSON(resp.Body, v)
}

func flattenItems(nested [][]listItem) []listItem {
	var items []listItem
	for _, row := range nested {
		items = append(items, row...)
	}
	return items
}

// ParseCount parses a count string like "4.418" (European thousands separator) to int.
func ParseCount(s string) int {
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, ",", "")
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// ParseDate parses a DD/MM/YYYY date string.
func ParseDate(s string) (time.Time, bool) {
	t, err := time.Parse("02/01/2006", s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}
