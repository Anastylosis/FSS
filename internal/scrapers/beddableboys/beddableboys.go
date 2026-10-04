// Package beddableboys scrapes beddableboys.com's public tour listing, a
// legacy NATS landing page that shows title and thumbnail only. See
// docs/scrapers.md.
package beddableboys

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "beddableboys"
	studioName = "Beddable Boys"
	listPath   = "/beddable-gay-twink-boys-bc/"
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?beddableboys\.com(?:/|$)`)

	cardRe  = regexp.MustCompile(`(?s)<div class="thumb"[^>]*>.*?</div>`)
	linkRe  = regexp.MustCompile(`href="([^"]*[?&;]scene=([a-z0-9_]+)[^"#]*)`)
	thumbRe = regexp.MustCompile(`<img src="([^"]+)"`)
	titleRe = regexp.MustCompile(`<h3>(.*?)</h3>`)
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
	return []string{"beddableboys.com", "beddableboys.com" + strings.TrimSuffix(listPath, "/") + "/{page}"}
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
		pageURL := fmt.Sprintf("%s%s%d", s.base, listPath, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseCards(body)
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}
		var scenes []models.Scene
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			scenes = append(scenes, models.Scene{
				ID:        c.id,
				SiteID:    siteID,
				StudioURL: studioURL,
				Title:     c.title,
				URL:       c.url,
				Thumbnail: c.thumb,
				Studio:    studioName,
				ScrapedAt: now,
			})
		}
		return scraper.PageResult{Scenes: scenes, Done: len(scenes) == 0}, nil
	})
}

type card struct{ id, title, url, thumb string }

func parseCards(body string) []card {
	var cards []card
	for _, block := range cardRe.FindAllString(body, -1) {
		link := linkRe.FindStringSubmatch(block)
		title := titleRe.FindStringSubmatch(block)
		if link == nil || title == nil {
			continue
		}
		c := card{id: link[2], url: html.UnescapeString(link[1]), title: strings.TrimSpace(html.UnescapeString(title[1]))}
		if m := thumbRe.FindStringSubmatch(block); m != nil {
			c.thumb = m[1]
		}
		cards = append(cards, c)
	}
	return cards
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
