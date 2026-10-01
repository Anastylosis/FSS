package crunchboy

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

const (
	defaultBase = "https://www.crunchboy.com"
	perPage     = 12
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "crunchboy" }

func (s *Scraper) Patterns() []string {
	return []string{
		"crunchboy.com/en/videos",
		"crunchboy.com",
		"crunchboy.com/en/videos/{studio}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?crunchboy\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	listPath := listingPath(studioURL)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, "crunchboy", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?page=%d", s.base, listPath, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items, totalPages := parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = item.toScene(studioURL, now)
		}
		return scraper.PageResult{
			Scenes: scenes,
			Total:  totalPages * perPage,
			Done:   page >= totalPages,
		}, nil
	})
}

type parsedItem struct {
	id          string
	url         string
	title       string
	description string
	thumbnail   string
	date        time.Time
	performers  []string
	duration    int
	studio      string
}

func (p *parsedItem) toScene(studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          p.id,
		SiteID:      "crunchboy",
		StudioURL:   studioURL,
		Title:       p.title,
		URL:         p.url,
		Description: p.description,
		Thumbnail:   p.thumbnail,
		Duration:    p.duration,
		Performers:  p.performers,
		Studio:      p.studio,
		Date:        p.date,
		ScrapedAt:   now,
	}
	if sc.Studio == "" {
		sc.Studio = "Crunchboy"
	}
	return sc
}

var (
	detailIDRe = regexp.MustCompile(`/en/videos/detail/(\d+)-`)
	durationRe = regexp.MustCompile(`(\d+)\s*min`)
	studioRe   = regexp.MustCompile(`text-uppercase">([^<]+)`)
	maxPageRe  = regexp.MustCompile(`[?&]page=(\d+)`)
)

func parseListing(body []byte) ([]parsedItem, int) {
	totalPages := 0
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		if n, _ := strconv.Atoi(string(m[1])); n > totalPages {
			totalPages = n
		}
	}

	var items []parsedItem
	for _, v := range parseutil.ExtractVideoObjects(body) {
		idMatch := detailIDRe.FindStringSubmatch(v.URL)
		if idMatch == nil {
			continue
		}
		p := parsedItem{
			id:          idMatch[1],
			url:         v.URL,
			title:       html.UnescapeString(v.Name),
			description: html.UnescapeString(v.Description),
			thumbnail:   v.ThumbnailURL,
			performers:  v.Actors,
		}
		if v.DatePublished != "" {
			if t, err := time.Parse("2006-01-02", v.DatePublished); err == nil {
				p.date = t.UTC()
			}
		}
		items = append(items, p)
	}

	enrichFromHTML(body, items)
	return items, totalPages
}

func enrichFromHTML(body []byte, items []parsedItem) {
	bodyStr := string(body)
	ldEnd := strings.Index(bodyStr, `"ItemList"`)
	if ldEnd < 0 {
		ldEnd = 0
	}
	closeTag := strings.Index(bodyStr[ldEnd:], `</script>`)
	if closeTag >= 0 {
		ldEnd += closeTag + len(`</script>`)
	}
	rest := bodyStr[ldEnd:]

	for i := range items {
		pattern := fmt.Sprintf(`detail/%s-`, items[i].id)
		start := strings.Index(rest, pattern)
		if start < 0 {
			continue
		}
		end := start + 2000
		if i+1 < len(items) {
			nextPattern := fmt.Sprintf(`detail/%s-`, items[i+1].id)
			if next := strings.Index(rest[start+len(pattern):], nextPattern); next >= 0 {
				end = start + len(pattern) + next
			}
		}
		if end > len(rest) {
			end = len(rest)
		}
		chunk := rest[start:end]

		if m := durationRe.FindStringSubmatch(chunk); m != nil {
			items[i].duration, _ = strconv.Atoi(m[1])
			items[i].duration *= 60
		}
		if m := studioRe.FindStringSubmatch(chunk); m != nil {
			items[i].studio = strings.TrimSpace(m[1])
		}
	}
}

func listingPath(studioURL string) string {
	u := strings.TrimRight(studioURL, "/")
	if strings.HasSuffix(u, "/en/videos") || strings.Contains(u, "/en/videos/") {
		i := strings.Index(u, "/en/videos")
		return u[i:]
	}
	return "/en/videos"
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
