package vixenutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain + "/videos",
		s.cfg.Domain + "/videos/{slug}",
		s.cfg.Domain + "/performers/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool {
	return s.matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

const perPage = 12

var (
	nextDataRe    = regexp.MustCompile(`<script id="__NEXT_DATA__"[^>]*>(.*?)</script>`)
	performerSlug = regexp.MustCompile(`/performers/([^/?]+)`)
)

type nextData struct {
	Props struct {
		PageProps pageProps `json:"pageProps"`
	} `json:"props"`
}

type pageProps struct {
	Edges      []edge `json:"edges"`
	Videos     []node `json:"videos"`
	TotalCount int    `json:"totalCount"`
	PageNum    int    `json:"pageNum"`
	Video      *video `json:"video"`
}

type edge struct {
	Node node `json:"node"`
}

type node struct {
	VideoID       string     `json:"videoId"`
	Title         string     `json:"title"`
	Slug          string     `json:"slug"`
	Site          string     `json:"site"`
	ReleaseDate   string     `json:"releaseDate"`
	ModelsSlugged []modelRef `json:"modelsSlugged"`
	Images        nodeImages `json:"images"`
}

type modelRef struct {
	Name string `json:"name"`
}

type nodeImages struct {
	Listing []image `json:"listing"`
}

type image struct {
	Src    string `json:"src"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type video struct {
	VideoID            string     `json:"videoId"`
	Title              string     `json:"title"`
	Slug               string     `json:"slug"`
	Site               string     `json:"site"`
	ReleaseDate        string     `json:"releaseDate"`
	Description        string     `json:"description"`
	RunLength          string     `json:"runLength"`
	RunLengthFormatted string     `json:"runLengthFormatted"`
	ModelsSlugged      []modelRef `json:"modelsSlugged"`
	Directors          []director `json:"directors"`
	Images             nodeImages `json:"images"`
}

type director struct {
	Name string `json:"name"`
}

func extractNextData(body []byte) (*nextData, error) {
	m := nextDataRe.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("__NEXT_DATA__ not found")
	}
	var nd nextData
	if err := json.Unmarshal(m[1], &nd); err != nil {
		return nil, fmt.Errorf("parse __NEXT_DATA__: %w", err)
	}
	return &nd, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if m := performerSlug.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: detected performer page", s.cfg.SiteID)
		s.scrapePerformerPage(ctx, studioURL, opts, out)
		return
	}

	s.scrapeListingPages(ctx, opts, out)
}

func (s *Scraper) scrapePerformerPage(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()

	body, err := s.fetchPage(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	nd, err := extractNextData(body)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	videos := nd.Props.PageProps.Videos
	if len(videos) == 0 {
		return
	}
	scraper.Debugf(1, "%s: found %d scenes on performer page", s.cfg.SiteID, len(videos))

	select {
	case out <- scraper.Progress(len(videos)):
	case <-ctx.Done():
		return
	}

	for _, v := range videos {
		id := v.VideoID
		if id == "" {
			continue
		}
		if opts.KnownIDs[id] {
			scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(s.nodeToScene(v, now)):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) scrapeListingPages(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()
	delay := opts.Delay
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	type workItem struct {
		node node
	}

	work := make(chan workItem, workers)
	var wg sync.WaitGroup
	scraper.Debugf(1, "%s: fetching detail pages with %d workers", s.cfg.SiteID, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if !scraper.Pace(ctx, delay) {
					return
				}
				scene := s.fetchAndBuildScene(ctx, item.node, now)
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
		if page > 1 && !scraper.Pace(ctx, delay) {
			break
		}
		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)

		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)
		pageURL := fmt.Sprintf("%s/videos?page=%d", s.base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		nd, err := extractNextData(body)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		pp := nd.Props.PageProps
		if len(pp.Edges) == 0 {
			break
		}

		if page == 1 && pp.TotalCount > 0 {
			scraper.Debugf(1, "%s: %d total scenes", s.cfg.SiteID, pp.TotalCount)
			cancelled := false
			select {
			case out <- scraper.Progress(pp.TotalCount):
			case <-ctx.Done():
				cancelled = true
			}
			if cancelled {
				break
			}
		}

		for _, e := range pp.Edges {
			id := e.Node.VideoID
			if id == "" {
				continue
			}
			if opts.KnownIDs[id] {
				scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				close(work)
				wg.Wait()
				return
			}
			select {
			case work <- workItem{node: e.Node}:
			case <-ctx.Done():
				close(work)
				wg.Wait()
				return
			}
		}

		if len(pp.Edges) < perPage {
			break
		}
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) fetchAndBuildScene(ctx context.Context, n node, now time.Time) models.Scene {
	scene := s.nodeToScene(n, now)

	detailURL := fmt.Sprintf("%s/videos/%s", s.base, n.Slug)
	body, err := s.fetchPage(ctx, detailURL)
	if err != nil {
		return scene
	}

	nd, err := extractNextData(body)
	if err != nil || nd.Props.PageProps.Video == nil {
		return scene
	}

	v := nd.Props.PageProps.Video
	if v.Description != "" {
		scene.Description = v.Description
	}
	if v.RunLength != "" {
		scene.Duration = parseutil.ParseDurationColon(v.RunLength)
	}
	if len(v.Directors) > 0 {
		scene.Director = v.Directors[0].Name
	}

	return scene
}

func (s *Scraper) nodeToScene(n node, now time.Time) models.Scene {
	var performers []string
	for _, m := range n.ModelsSlugged {
		if m.Name != "" {
			performers = append(performers, m.Name)
		}
	}

	var thumb string
	for _, img := range n.Images.Listing {
		if img.Width > img.Height {
			thumb = img.Src
			break
		}
	}
	if thumb == "" && len(n.Images.Listing) > 0 {
		thumb = n.Images.Listing[0].Src
	}

	var date time.Time
	if n.ReleaseDate != "" {
		if t, err := time.Parse(time.RFC3339, n.ReleaseDate); err == nil {
			date = t.UTC()
		}
	}

	return models.Scene{
		ID:         n.VideoID,
		SiteID:     s.cfg.SiteID,
		StudioURL:  s.base,
		Title:      n.Title,
		URL:        fmt.Sprintf("%s/videos/%s", s.base, n.Slug),
		Thumbnail:  thumb,
		Date:       date,
		Performers: performers,
		Studio:     s.cfg.StudioName,
		ScrapedAt:  now,
	}
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
