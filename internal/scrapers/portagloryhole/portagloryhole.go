package portagloryhole

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const perPage = 50

type Scraper struct {
	client *http.Client
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func New() *Scraper {
	jar, _ := cookiejar.New(nil)
	c := httpx.NewClient(30 * time.Second)
	c.Jar = jar
	u, _ := url.Parse("https://www.portagloryhole.com")
	jar.SetCookies(u, []*http.Cookie{
		{Name: "americancumdolls_adult_warning", Value: "1"},
	})
	return &Scraper{client: c}
}

func init() { scraper.Register(New()) }

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?portagloryhole\.com(?:/|$)`)

func (s *Scraper) ID() string         { return "portagloryhole" }
func (s *Scraper) Patterns() []string { return []string{"portagloryhole.com/"} }
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
	scraper.Debugf(1, "portagloryhole: starting scrape")

	now := time.Now().UTC()
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}

		scraper.Debugf(1, "portagloryhole: fetching page %d", page)
		body, err := s.fetchListing(ctx, page)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("listing page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		cards, totalPages := parseListing(body)
		if len(cards) == 0 {
			break
		}

		if page == 1 && totalPages > 0 {
			total := totalPages * perPage
			if total > 0 {
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
		}

		for _, c := range cards {
			if opts.KnownIDs[c.id] {
				scraper.Debugf(1, "portagloryhole: hit known ID %s, stopping early", c.id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case out <- scraper.Scene(c.toScene(studioURL, now)):
			case <-ctx.Done():
				return
			}
		}

		if totalPages > 0 && page >= totalPages {
			break
		}
	}
}

func (s *Scraper) fetchListing(ctx context.Context, page int) ([]byte, error) {
	form := url.Values{
		"aParams[contentType]":              {"posts"},
		"aParams[user_ids][0]":              {"12"},
		"aParams[sorting]":                  {"date_and_id"},
		"aParams[max_results]":              {strconv.Itoa(perPage)},
		"aParams[page]":                     {strconv.Itoa(page)},
		"aParams[aStatus][0]":               {"0"},
		"aParams[content_type][0]":          {"4"},
		"aParams[aVisibility][0]":           {"0"},
		"aParams[aVisibility][1]":           {"1"},
		"aParams[show_hidden]":              {"false"},
		"aParams[ajax_pagination]":          {"true"},
		"aParams[hide_x_rated_content]":     {"false"},
		"aParams[show_from_disabled_users]": {"false"},
		"aParams[show_pagination]":          {"true"},
		"aParams[item_template]":            {"MMFrontendBundle:www.americancumdolls.com:item/post.html.twig"},
		"aParams[results_template]":         {"@MMCore/ContentBlock/results.html.twig"},
		"aParams[pagination_template]":      {"@MMCore/ContentBlock/pagination.html.twig"},
		"aParams[container_id]":             {"1"},
	}

	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		Method: http.MethodPost,
		URL:    "https://www.portagloryhole.com/op/results/paginate",
		Body:   []byte(form.Encode()),
		Headers: map[string]string{
			"Content-Type":     "application/x-www-form-urlencoded",
			"X-Requested-With": "XMLHttpRequest",
			"User-Agent":       httpx.UserAgentFirefox,
			"Referer":          "https://www.portagloryhole.com/",
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

type listingCard struct {
	id        string
	title     string
	performer string
	date      time.Time
	duration  int
	thumbnail string
}

func (c listingCard) toScene(studioURL string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        c.id,
		SiteID:    "portagloryhole",
		StudioURL: studioURL,
		Title:     c.title,
		URL:       "https://www.portagloryhole.com/post/details/" + c.id,
		Date:      c.date,
		Duration:  c.duration,
		Thumbnail: c.thumbnail,
		Studio:    "Porta Gloryhole",
		ScrapedAt: now,
	}
	if c.performer != "" {
		scene.Performers = []string{c.performer}
	}
	return scene
}

var (
	postIDRe    = regexp.MustCompile(`data-post-id="(\d+)"`)
	titleRe     = regexp.MustCompile(`<h1>\s*<a[^>]*title="([^"]*)"`)
	performerRe = regexp.MustCompile(`<h2>\s*<a[^>]*title="([^"]*)"`)
	dateRe      = regexp.MustCompile(`class="posted_on">([^<]+)`)
	durationRe  = regexp.MustCompile(`fa-video"></i>\s*(\d+:\d+)`)
	thumbRe     = regexp.MustCompile(`data-media-poster="([^"]+)"`)
	lastPageRe  = regexp.MustCompile(`data-page="(\d+)"[^>]*title="last"`)
	cardSep     = []byte(`<div class="post_item video"`)
)

func parseListing(body []byte) ([]listingCard, int) {
	totalPages := 0
	if m := lastPageRe.FindSubmatch(body); m != nil {
		totalPages, _ = strconv.Atoi(string(m[1]))
	}

	parts := splitCards(body)
	cards := make([]listingCard, 0, len(parts))
	seen := make(map[string]bool, len(parts))

	for _, part := range parts {
		m := postIDRe.FindSubmatch(part)
		if m == nil {
			continue
		}
		id := string(m[1])
		if seen[id] {
			continue
		}
		seen[id] = true

		c := listingCard{id: id}
		if m := titleRe.FindSubmatch(part); m != nil {
			c.title = html.UnescapeString(string(m[1]))
		}
		if m := performerRe.FindSubmatch(part); m != nil {
			c.performer = html.UnescapeString(string(m[1]))
		}
		if m := dateRe.FindSubmatch(part); m != nil {
			if t, err := time.Parse("Jan 2, 2006", strings.TrimSpace(string(m[1]))); err == nil {
				c.date = t.UTC()
			}
		}
		if m := durationRe.FindSubmatch(part); m != nil {
			c.duration = parseutil.ParseDurationColon(string(m[1]))
		}
		if m := thumbRe.FindSubmatch(part); m != nil {
			c.thumbnail = html.UnescapeString(string(m[1]))
		}

		cards = append(cards, c)
	}
	return cards, totalPages
}

func splitCards(body []byte) [][]byte {
	parts := bytes.Split(body, cardSep)
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}
