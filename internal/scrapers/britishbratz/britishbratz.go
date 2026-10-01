// Package britishbratz scrapes britishbratz.com, a UTG network site. The tour
// is a Laravel Livewire app: /updates/videos lists 36 cards per page, paged by
// the ?updates_page= query parameter, newest first. The age gate is
// client-side only, so plain GETs need no session.
package britishbratz

import (
	"bytes"
	"context"
	"errors"
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

const (
	siteID   = "britishbratz"
	siteBase = "https://www.britishbratz.com"
	pageSize = 36
)

type Scraper struct {
	client *http.Client
	base   string
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

func init() { scraper.Register(New()) }

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?britishbratz\.com(?:/|$)`)

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{"britishbratz.com/"}
}
func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardStart  = []byte(`<article`)
	cardEnd    = []byte(`</article>`)
	cardHrefRe = regexp.MustCompile(`href="[^"]*/updates/previews/videos/([^"/?#]+)"`)
	titleRe    = regexp.MustCompile(`(?s)<h2[^>]*>\s*<a[^>]*>\s*(.*?)\s*</a>`)
	imgSrcRe   = regexp.MustCompile(`(?s)<img\s+src="([^"]+)"`)
	dateRe     = regexp.MustCompile(`<h3 class="[^"]*text-right[^"]*">\s*([^<]+?)\s*</h3>`)
	uuidRe     = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
	categoryRe = regexp.MustCompile(`/sub-category/[^"]*">([^<]+)</a>`)
	modelRe    = regexp.MustCompile(`/bratz/[^"]*">([^<]+)</a>`)
	lastPageRe = regexp.MustCompile(`gotoPage\((\d+), 'updates_page'\)`)
	nextPageRe = regexp.MustCompile(`nextPage\('updates_page'\)`)
)

var errNoCards = errors.New("no scene cards found on listing page")

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/updates/videos?updates_page=%d", s.base, page)
		body, err := s.fetchHTML(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}

		scenes := s.parseListingPage(body, studioURL)
		more := nextPageRe.Match(body)
		if len(scenes) == 0 {
			if page == 1 || more {
				return scraper.PageResult{}, scraper.ParseError(u, errNoCards)
			}
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			total = estimateTotal(body)
		}

		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   !more,
		}, nil
	})
}

func (s *Scraper) parseListingPage(body []byte, studioURL string) []models.Scene {
	parts := bytes.Split(body, cardStart)
	now := time.Now().UTC()
	var scenes []models.Scene

	for _, card := range parts[min(1, len(parts)):] {
		if end := bytes.Index(card, cardEnd); end >= 0 {
			card = card[:end]
		}
		href := cardHrefRe.FindSubmatch(card)
		if href == nil {
			continue
		}
		slug := string(href[1])

		var title, thumbnail, id string
		if m := titleRe.FindSubmatch(card); m != nil {
			title = html.UnescapeString(strings.TrimSpace(string(m[1])))
		}
		if title == "" {
			continue
		}

		if m := imgSrcRe.FindSubmatch(card); m != nil {
			thumbnail = html.UnescapeString(string(m[1]))
		}
		if m := uuidRe.FindStringSubmatch(thumbnail); m != nil {
			id = m[1]
		}
		if id == "" {
			id = slugify(title)
		}

		scene := models.Scene{
			ID:        id,
			SiteID:    siteID,
			StudioURL: studioURL,
			Title:     title,
			URL:       s.base + "/updates/previews/videos/" + slug,
			Thumbnail: thumbnail,
			Studio:    "British Bratz",
			ScrapedAt: now,
		}

		if m := dateRe.FindSubmatch(card); m != nil {
			if t, err := time.Parse("2 January 2006", string(m[1])); err == nil {
				scene.Date = t.UTC()
			}
		}

		scene.Tags = names(categoryRe, card)
		scene.Performers = names(modelRe, card)

		scenes = append(scenes, scene)
	}
	return scenes
}

func names(re *regexp.Regexp, card []byte) []string {
	var out []string
	for _, m := range re.FindAllSubmatch(card, -1) {
		if n := html.UnescapeString(strings.TrimSpace(string(m[1]))); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func estimateTotal(body []byte) int {
	maxPage := 0
	for _, m := range lastPageRe.FindAllSubmatch(body, -1) {
		if n, _ := strconv.Atoi(string(m[1])); n > maxPage {
			maxPage = n
		}
	}
	return maxPage * pageSize
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == ' ' || c == '-' || c == '_':
			b.WriteByte('-')
		}
	}
	return b.String()
}

func (s *Scraper) fetchHTML(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
