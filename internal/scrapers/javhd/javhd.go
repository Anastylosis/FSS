// Package javhd scrapes javhd.com, a Vue tour whose listing is served as a
// rendered template fragment over XHR. See docs/scrapers.md.
package javhd

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "javhd"
	studioName = "JAVHD"
	// listingPath is the "just added" listing; page numbers are a path
	// segment, not a query parameter.
	listingPath = "/en/japanese-porn-videos/justadded/all/"
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?javhd\.com(?:/|$)`)

	cardRe  = regexp.MustCompile(`(?s)<thumb-component\b(.*?)</thumb-component>`)
	attrRe  = regexp.MustCompile(`([a-z-]+)="([^"]*)"`)
	tagStip = regexp.MustCompile(`<[^>]+>`)
)

// Scraper implements scraper.StudioScraper for javhd.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the JAVHD scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://javhd.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string { return []string{"javhd.com"} }

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// listingResponse is what the tour answers an XHR with: the thumbs rendered as
// a template fragment, plus the counts the pager needs.
type listingResponse struct {
	Status   int    `json:"status"`
	Template string `json:"template"`
	Results  int    `json:"results_count"`
	PerPage  int    `json:"per_page"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	// The listing's order is neither date nor id — page 2 carries higher ids
	// than page 1 — so a known scene says nothing about what follows it and
	// the early-stop hint is dropped rather than truncating the walk.
	opts.KnownIDs = nil

	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s%d", s.base, listingPath, page)
		var resp listingResponse
		if err := s.fetchJSON(ctx, pageURL, &resp); err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(resp.Template)
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no thumbs in the listing template"))
		}

		scenes := make([]models.Scene, 0, len(cards))
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			scenes = append(scenes, s.toScene(c, studioURL, now))
		}
		if len(scenes) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		total := 0
		if page == 1 {
			total = resp.Results
		}
		// Past the last page the tour answers HTTP 404, so the walk has to
		// stop on the counts it was given rather than on an empty page.
		done := resp.PerPage > 0 && page*resp.PerPage >= resp.Results
		return scraper.PageResult{Scenes: scenes, Total: total, Done: done}, nil
	})
}

// card is one thumb-component, which is the whole record: the tour's detail
// page adds only a tag list and a model that is usually "N/A", at one request
// per scene across ~18,600 of them, so it is deliberately not fetched.
type card struct {
	id      string
	link    string
	thumb   string
	preview string
	title   string
	views   int
	likes   int
}

// parseListing reads the thumbs out of the rendered template fragment.
func parseListing(template string) []card {
	ms := cardRe.FindAllStringSubmatch(template, -1)
	cards := make([]card, 0, len(ms))
	for _, m := range ms {
		attrs := map[string]string{}
		for _, a := range attrRe.FindAllStringSubmatch(m[1], -1) {
			attrs[a[1]] = html.UnescapeString(a[2])
		}
		if attrs["type-thumb"] != "video" || attrs["video-id"] == "" {
			continue
		}
		c := card{
			id:      attrs["video-id"],
			link:    attrs["link-content"],
			thumb:   attrs["url-thumb"],
			preview: attrs["video-preview"],
			title:   cleanText(attrs["title"]),
		}
		c.views, _ = strconv.Atoi(attrs["views"])
		c.likes, _ = strconv.Atoi(attrs["likes"])
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) toScene(c card, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:        c.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     c.title,
		URL:       s.sceneURL(c),
		Thumbnail: c.thumb,
		Preview:   c.preview,
		Views:     c.views,
		Likes:     c.likes,
		ScrapedAt: now,
	}
}

// sceneURL re-homes the absolute link the template carries onto the host being
// scraped, and falls back to the id path when the card carries none.
func (s *Scraper) sceneURL(c card) string {
	if c.link != "" {
		if u, err := url.Parse(c.link); err == nil && u.Path != "" {
			return s.base + u.Path
		}
	}
	return fmt.Sprintf("%s/en/id/%s", s.base, c.id)
}

func (s *Scraper) fetchJSON(ctx context.Context, pageURL string, v any) error {
	h := httpx.XHRHeaders(httpx.UserAgentFirefox, s.base)
	h["Accept"] = "application/json, text/javascript, */*; q=0.01"
	h["X-Requested-With"] = "XMLHttpRequest"
	return httpx.DoJSON(ctx, s.client, httpx.Request{URL: pageURL, Headers: h}, v)
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStip.ReplaceAllString(s, " "))), " ")
}
