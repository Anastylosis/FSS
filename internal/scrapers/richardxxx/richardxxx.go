// Package richardxxx scrapes richard.xxx, a Naughty America suite tour that
// carries several gay brands. See docs/scrapers.md.
package richardxxx

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "richardxxx"
	defaultPath   = "/new-porn-videos"
	detailWorkers = 4
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?richard\.xxx(?:/|$|\?)`)
	siteRe  = regexp.MustCompile(`^/site/([^/?#]+)`)
	starRe  = regexp.MustCompile(`^/pornstar/([^/?#]+)`)

	cardSplitRe = regexp.MustCompile(`<div class="col-[^"]*\bscene"`)
	sceneHref   = regexp.MustCompile(`href="([^"]*/scene/[^"?#]*-(\d+))(?:\?[^"]*)?"`)
	thumbRe     = regexp.MustCompile(`<img src="([^"]+)"`)
	durationRe  = regexp.MustCompile(`class="duration">\s*(\d+)\s*min`)
	actorsRe    = regexp.MustCompile(`(?s)class="scene-actors">(.*?)</span>`)
	anchorRe    = regexp.MustCompile(`(?s)<a [^>]*>(.*?)</a>`)
	brandRe     = regexp.MustCompile(`(?s)<a href="[^"]*/site/[^"]*">\s*<span class="scene-title">(.*?)</span>`)
	dateRe      = regexp.MustCompile(`class="scene-date">\s*([^<]+?)\s*<`)
	tagRe       = regexp.MustCompile(`(?s)<a href="[^"]*/gay-porn-videos/[^"]*"[^>]*>(.*?)</a>`)
	ldJSONRe    = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

var dateLayouts = []string{"Jan 2, 2006", "Jan 02, 2006"}

// Scraper implements scraper.StudioScraper for richard.xxx.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the RICHARD.XXX scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://www.richard.xxx"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"richard.xxx",
		"richard.xxx/new-porn-videos",
		"richard.xxx/site/{brand}",
		"richard.xxx/pornstar/{performer}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one listing entry; everything but the description is on it.
type card struct {
	id         string
	url        string
	brand      string
	title      string
	date       time.Time
	duration   int
	performers []string
	tags       []string
	thumb      string
}

// listingPath maps an operator URL onto the listing path: a brand, a
// performer, or the whole network.
func listingPath(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return defaultPath
	}
	if m := siteRe.FindStringSubmatch(u.Path); m != nil {
		return "/site/" + m[1]
	}
	if m := starRe.FindStringSubmatch(u.Path); m != nil {
		return "/pornstar/" + m[1]
	}
	return defaultPath
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	path := listingPath(studioURL)
	scraper.Debugf(1, "%s: listing %s", siteID, path)
	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?page=%d", s.base, path, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 && path == defaultPath {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}

		var fresh []card
		for _, c := range cards {
			if !seen[c.id] {
				seen[c.id] = true
				fresh = append(fresh, c)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		scenes := make([]models.Scene, len(fresh))
		for i, c := range fresh {
			scenes[i] = s.toScene(c, studioURL, now)
		}
		s.enrich(ctx, scenes, opts, out)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// parseListing reads the scene cards of a listing page.
func parseListing(body string) []card {
	marks := cardSplitRe.FindAllStringIndex(body, -1)
	cards := make([]card, 0, len(marks))
	for i, m := range marks {
		end := len(body)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		block := body[m[0]:end]

		href := sceneHref.FindStringSubmatch(block)
		if href == nil {
			continue
		}
		c := card{id: href[2], url: href[1]}
		if b := brandRe.FindStringSubmatch(block); b != nil {
			c.brand = cleanText(b[1])
		}
		if a := actorsRe.FindStringSubmatch(block); a != nil {
			for _, p := range anchorRe.FindAllStringSubmatch(a[1], -1) {
				if name := cleanText(p[1]); name != "" {
					c.performers = append(c.performers, name)
				}
			}
		}
		if d := dateRe.FindStringSubmatch(block); d != nil {
			if t, err := parseutil.TryParseDate(cleanText(d[1]), dateLayouts...); err == nil {
				c.date = t
			}
		}
		if d := durationRe.FindStringSubmatch(block); d != nil {
			if n, err := strconv.Atoi(d[1]); err == nil {
				c.duration = n * 60
			}
		}
		for _, t := range tagRe.FindAllStringSubmatch(block, -1) {
			if tag := cleanText(t[1]); tag != "" {
				c.tags = append(c.tags, tag)
			}
		}
		if th := thumbRe.FindStringSubmatch(block); th != nil {
			c.thumb = html.UnescapeString(th[1])
		}
		c.title = buildTitle(c.performers, c.brand)
		cards = append(cards, c)
	}
	return cards
}

// buildTitle mirrors the site's own "A & B in Brand" naming.
func buildTitle(performers []string, brand string) string {
	switch {
	case len(performers) == 0:
		return brand
	case brand == "":
		return strings.Join(performers, " & ")
	}
	return strings.Join(performers, " & ") + " in " + brand
}

func (s *Scraper) toScene(c card, studioURL string, now time.Time) models.Scene {
	thumb := c.thumb
	if strings.HasPrefix(thumb, "//") {
		thumb = "https:" + thumb
	}
	return models.Scene{
		ID:         c.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     c.brand,
		Title:      c.title,
		URL:        s.rebase(c.url),
		Date:       c.date,
		Duration:   c.duration,
		Performers: c.performers,
		Tags:       c.tags,
		Thumbnail:  thumb,
		ScrapedAt:  now,
	}
}

// rebase keeps the scene URL on the host being scraped, so the scraper stays
// pointable at a test server.
func (s *Scraper) rebase(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	return s.base + p.Path
}

type videoObject struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// parseDetail reads the VideoObject from a scene page's JSON-LD.
func parseDetail(body string) (videoObject, bool) {
	for _, m := range ldJSONRe.FindAllStringSubmatch(body, -1) {
		var v videoObject
		if err := json.Unmarshal([]byte(m[1]), &v); err == nil && (v.Name != "" || v.Description != "") {
			return v, true
		}
	}
	return videoObject{}, false
}

// enrich adds the description, which only the scene page carries.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range scenes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			if v, ok := parseDetail(body); ok {
				scenes[i].Description = cleanText(v.Description)
			}
		}(i)
	}
	wg.Wait()
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

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}
