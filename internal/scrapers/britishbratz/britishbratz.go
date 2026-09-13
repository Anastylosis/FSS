// Package britishbratz scrapes britishbratz.com, a Glamose/UTG network site
// with a Bootstrap 3 template and POST-based age gate. Uses the same
// /updates/videos/{page} URL pattern as UTG sites but with different card HTML.
package britishbratz

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const siteBase = "https://www.britishbratz.com"

type Scraper struct {
	client *http.Client
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func New() *Scraper {
	jar, _ := cookiejar.New(nil)
	c := httpx.NewClient(30 * time.Second)
	c.Jar = jar
	return &Scraper{client: c}
}

func init() { scraper.Register(New()) }

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?britishbratz\.com(?:/|$)`)

func (s *Scraper) ID() string { return "britishbratz" }
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
	cardRe     = regexp.MustCompile(`(?s)<div class="col-sm-4 single_update">(.*?)<div class="clearfix">`)
	imgAltRe   = regexp.MustCompile(`<img[^>]+alt="([^"]+)"`)
	imgSrcRe   = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	timeRe     = regexp.MustCompile(`<time>([^<]+)</time>`)
	uuidRe     = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
	categoryRe = regexp.MustCompile(`/sub-category/[^"]*">([^<]+)</a>`)
	lastPageRe = regexp.MustCompile(`/updates/videos/(\d+)"`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if err := s.passAgeGate(ctx); err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("age gate: %w", err)):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "britishbratz: age gate passed")

	scraper.Paginate(ctx, opts, "britishbratz", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/updates/videos/%d", siteBase, page)
		body, err := s.fetchHTML(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}

		scenes := parseListingPage(body, studioURL)

		total := 0
		if page == 1 {
			total = estimateTotal(body)
		}

		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   len(scenes) == 0,
		}, nil
	})
}

func parseListingPage(body []byte, studioURL string) []models.Scene {
	cards := cardRe.FindAllSubmatch(body, -1)
	now := time.Now().UTC()
	var scenes []models.Scene

	for _, card := range cards {
		content := card[1]

		var title, thumbnail, id string

		if m := imgAltRe.FindSubmatch(content); m != nil {
			title = html.UnescapeString(strings.TrimSpace(string(m[1])))
		}
		if title == "" {
			continue
		}

		if m := imgSrcRe.FindSubmatch(content); m != nil {
			src := string(m[1])
			if !strings.Contains(src, "video_bg_small") {
				thumbnail = src
			}
		}

		if thumbnail != "" {
			if m := uuidRe.FindStringSubmatch(thumbnail); m != nil {
				id = m[1]
			}
		}
		if id == "" {
			id = slugify(title)
		}

		scene := models.Scene{
			ID:        id,
			SiteID:    "britishbratz",
			StudioURL: studioURL,
			Title:     title,
			URL:       siteBase + "/join",
			Thumbnail: thumbnail,
			Studio:    "British Bratz",
			ScrapedAt: now,
		}

		if m := timeRe.FindSubmatch(content); m != nil {
			if t, err := time.Parse("2 January 2006", strings.TrimSpace(string(m[1]))); err == nil {
				scene.Date = t.UTC()
			}
		}

		var tags []string
		for _, m := range categoryRe.FindAllSubmatch(content, -1) {
			tag := html.UnescapeString(strings.TrimSpace(string(m[1])))
			if tag != "" {
				tags = append(tags, tag)
			}
		}
		scene.Tags = tags

		scenes = append(scenes, scene)
	}
	return scenes
}

func estimateTotal(body []byte) int {
	maxPage := 0
	for _, m := range lastPageRe.FindAllSubmatch(body, -1) {
		if n := atoi(string(m[1])); n > maxPage {
			maxPage = n
		}
	}
	if maxPage > 0 {
		return maxPage * 20
	}
	return 0
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
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

func (s *Scraper) passAgeGate(ctx context.Context) error {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		Method: "POST",
		URL:    siteBase + "/accessSite/accept",
		Headers: map[string]string{
			"User-Agent":   httpx.UserAgentFirefox,
			"Content-Type": "application/x-www-form-urlencoded",
		},
		Body: []byte("MM_insert=age_approved"),
	})
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func (s *Scraper) fetchHTML(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: rawURL,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
