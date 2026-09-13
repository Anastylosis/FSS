package analacrobats

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const siteID = "analacrobats"

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?analacrobats\.com(?:/|$)`)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://www.analacrobats.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{
		"analacrobats.com",
		"analacrobats.com/models/{slug}.html",
	}
}

var modelRe = regexp.MustCompile(`/models/[^/]+\.html`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardRe      = regexp.MustCompile(`(?s)<div class="item-col col -video"[^>]*>(.+?)</div>\s*</div>\s*</div>\s*</div>`)
	sceneURLRe  = regexp.MustCompile(`<a href="([^"]*?/video/[^"]+)"`)
	sceneIDRe   = regexp.MustCompile(`-(\d+)\.html`)
	titleRe     = regexp.MustCompile(`<span class="item-name">([^<]+)</span>`)
	thumbRe     = regexp.MustCompile(`data-src="([^"]+)"`)
	dateRe      = regexp.MustCompile(`Date:\s*(\d{2}\.\d{2}\.\d{4})`)
	durationRe  = regexp.MustCompile(`Time:\s*(\d+:\d{2}(?::\d{2})?)`)
	performerRe = regexp.MustCompile(`<a\s+title="([^"]+)"\s+href="[^"]*?/models/`)
	maxPageRe   = regexp.MustCompile(`page(\d+)\.html`)
)

type sceneItem struct {
	id         string
	title      string
	url        string
	thumb      string
	date       time.Time
	duration   int
	performers []string
}

func parseListingPage(body []byte) []sceneItem {
	matches := cardRe.FindAllSubmatch(body, -1)
	items := make([]sceneItem, 0, len(matches))

	for _, m := range matches {
		block := string(m[1])
		var item sceneItem

		if sm := sceneURLRe.FindStringSubmatch(block); sm != nil {
			item.url = sm[1]
			if idm := sceneIDRe.FindStringSubmatch(item.url); idm != nil {
				item.id = idm[1]
			}
		}
		if item.id == "" {
			continue
		}

		if sm := titleRe.FindStringSubmatch(block); sm != nil {
			item.title = html.UnescapeString(strings.TrimSpace(sm[1]))
		}

		if sm := thumbRe.FindStringSubmatch(block); sm != nil {
			thumb := sm[1]
			if strings.HasPrefix(thumb, "//") {
				thumb = "https:" + thumb
			}
			item.thumb = thumb
		}

		if sm := dateRe.FindStringSubmatch(block); sm != nil {
			if t, err := time.Parse("02.01.2006", sm[1]); err == nil {
				item.date = t.UTC()
			}
		}

		if sm := durationRe.FindStringSubmatch(block); sm != nil {
			item.duration = parseutil.ParseDurationColon(sm[1])
		}

		for _, pm := range performerRe.FindAllStringSubmatch(block, -1) {
			name := strings.TrimSpace(pm[1])
			if name != "" {
				item.performers = append(item.performers, name)
			}
		}

		items = append(items, item)
	}
	return items
}

func estimateTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()

	if modelRe.MatchString(studioURL) {
		s.scrapeModelPage(ctx, studioURL, opts, out, now)
	} else {
		s.scrapeListing(ctx, studioURL, opts, out, now)
	}
}

func (s *Scraper) scrapeModelPage(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	body, err := s.fetchPage(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scenes := parseListingPage(body)
	if len(scenes) > 0 {
		select {
		case out <- scraper.Progress(len(scenes)):
		case <-ctx.Done():
			return
		}
	}
	for _, item := range scenes {
		if opts.KnownIDs[item.id] {
			continue
		}
		select {
		case out <- scraper.Scene(item.toScene(studioURL, s.base, now)):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) scrapeListing(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	scraper.Paginate(ctx, opts, "analacrobats", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + "/most-recent/"
		if page > 1 {
			pageURL += fmt.Sprintf("page%d.html", page)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListingPage(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			total = estimateTotal(body, len(items))
		}

		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = item.toScene(studioURL, s.base, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func (item sceneItem) toScene(studioURL, base string, now time.Time) models.Scene {
	url := item.url
	if strings.HasPrefix(url, "/") {
		url = base + url
	}
	return models.Scene{
		ID:         item.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      item.title,
		URL:        url,
		Thumbnail:  item.thumb,
		Date:       item.date,
		Duration:   item.duration,
		Performers: item.performers,
		Studio:     "Anal Acrobats",
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
