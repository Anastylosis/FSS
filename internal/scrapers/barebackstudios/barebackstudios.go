package barebackstudios

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const siteBase = "https://barebackstudios.com"

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	jar, _ := cookiejar.New(nil)
	c := httpx.NewClient(30 * time.Second)
	c.Jar = jar
	return &Scraper{
		client: c,
		base:   siteBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "barebackstudios" }

func (s *Scraper) Patterns() []string {
	return []string{"barebackstudios.com"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?barebackstudios\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardRe  = regexp.MustCompile(`(?s)<div class="video_\d+[^"]*">\s*<div class="thumbnail[^>]*"(.*?)</div>\s*</div>\s*</div>\s*</div>`)
	idRe    = regexp.MustCompile(`data-video-id="(\d+)"`)
	titleRe = regexp.MustCompile(`data-title="([^"]*)"`)
	descRe  = regexp.MustCompile(`(?s)data-description="(.*?)"`)
	dateRe  = regexp.MustCompile(`data-created="([^"]*)"`)
	thumbRe = regexp.MustCompile(`data-thumb="([^"]*)"`)
	kwRe    = regexp.MustCompile(`data-keywords="([^"]*)"`)
	actorRe = regexp.MustCompile(`data-actors="([^"]*)"`)
	catRe   = regexp.MustCompile(`data-category="([^"]*)"`)
	priceRe = regexp.MustCompile(`<small[^>]*>\s*\$\s*([\d.]+)\s*</small>`)

	relativeDateRe = regexp.MustCompile(`(\d+)\s+(year|month|week|day)s?`)
)

type entry struct {
	id          string
	title       string
	description string
	thumb       string
	performers  []string
	tags        []string
	categories  []string
	price       float64
	relDate     string
}

func parseListing(body string) []entry {
	cards := cardRe.FindAllStringSubmatch(body, -1)
	out := make([]entry, 0, len(cards))
	for _, m := range cards {
		card := m[0]
		e := entry{}
		if v := idRe.FindStringSubmatch(card); v != nil {
			e.id = v[1]
		}
		if v := titleRe.FindStringSubmatch(card); v != nil {
			e.title = html.UnescapeString(strings.TrimSpace(v[1]))
		}
		if v := descRe.FindStringSubmatch(card); v != nil {
			e.description = html.UnescapeString(strings.TrimSpace(v[1]))
		}
		if v := thumbRe.FindStringSubmatch(card); v != nil {
			e.thumb = v[1]
		}
		if v := kwRe.FindStringSubmatch(card); v != nil && v[1] != "" {
			e.tags = splitTrim(v[1])
		}
		if v := actorRe.FindStringSubmatch(card); v != nil && v[1] != "" {
			e.performers = splitTrim(v[1])
		}
		if v := catRe.FindStringSubmatch(card); v != nil && v[1] != "" {
			e.categories = splitTrim(v[1])
		}
		if v := priceRe.FindStringSubmatch(card); v != nil {
			e.price, _ = strconv.ParseFloat(v[1], 64)
		}
		if v := dateRe.FindStringSubmatch(card); v != nil {
			e.relDate = v[1]
		}
		if e.id != "" {
			out = append(out, e)
		}
	}
	return out
}

func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseRelativeDate(s string, now time.Time) time.Time {
	var years, months, weeks, days int
	for _, m := range relativeDateRe.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "year":
			years = n
		case "month":
			months = n
		case "week":
			weeks = n
		case "day":
			days = n
		}
	}
	return now.AddDate(-years, -months, -(weeks*7 + days))
}

func (s *Scraper) run(ctx context.Context, _ string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if err := s.verifyAge(ctx); err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("age verification: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	seen := make(map[string]bool)
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, "barebackstudios", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		entries := parseListing(body)

		// Dedup: if every entry on this page was already seen on a
		// previous page, the site is looping — stop.
		allSeen := true
		for _, e := range entries {
			if !seen[e.id] {
				allSeen = false
				break
			}
		}
		if allSeen {
			return scraper.PageResult{}, nil
		}

		var scenes []models.Scene
		for _, e := range entries {
			if seen[e.id] {
				continue
			}
			seen[e.id] = true
			scenes = append(scenes, toScene(s.base, e, now))
		}
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func toScene(base string, e entry, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          e.id,
		SiteID:      "barebackstudios",
		StudioURL:   base,
		Title:       e.title,
		URL:         base + "/en/",
		Description: e.description,
		Thumbnail:   e.thumb,
		Performers:  e.performers,
		Tags:        e.tags,
		Categories:  e.categories,
		Studio:      "Bare Back Studios",
		ScrapedAt:   now,
	}
	if e.relDate != "" {
		sc.Date = parseRelativeDate(e.relDate, now)
	}
	if e.price > 0 {
		sc.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: e.price,
		})
	}
	return sc
}

func (s *Scraper) verifyAge(ctx context.Context) error {
	// httpx.Do treats Body == nil as a GET; force POST explicitly.
	// Non-2xx already returns *httpx.StatusError so callers can
	// `errors.As` on it.
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		Method: http.MethodPost,
		URL:    s.base + "/api/v1/site/verify_age/",
		Headers: map[string]string{
			"User-Agent":   httpx.UserAgentFirefox,
			"Content-Type": "application/json",
		},
	})
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func (s *Scraper) fetchPage(ctx context.Context, page int) (string, error) {
	u := fmt.Sprintf("%s/en/?page=%d", s.base, page)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	b, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading body: %w", err)
	}
	return string(b), nil
}
