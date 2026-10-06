// Package collegeuniform scrapes college-uniform.com — a Darkreach
// Communications site running the `update_details` + `data-setid` Elevated X
// template (same family as goldwinpass, but with its own field layout).
//
// Card markup:
//
//	<div class="update_details" data-setid="3894">
//	  <a href=".../updates/{title-slug}.html">
//	    <img class="stdimage update_thumb thumbs" src="content/.../1.jpg" src0_1x="..." />
//	  </a>
//	  <a href=".../updates/{title-slug}.html">Title</a>
//	  <span class="update_models">
//	    <a href=".../models/{Model-Name}.html">Model Name</a>
//	  </span>
//	  <div class="update_counts">21&nbsp;min&nbsp;of video</div>
//	  <div class="cell update_date">05/27/2026</div>
//	</div>
//
// Pagination: `/categories/updates_{N}_d.html` — past-end pages return zero
// `data-setid` matches (stop signal).
package collegeuniform

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
	"github.com/Anastylosis/FSS/scraper"
)

const defaultBase = "https://college-uniform.com"

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

func (s *Scraper) ID() string { return "collegeuniform" }

func (s *Scraper) Patterns() []string {
	return []string{"college-uniform.com", "college-uniform.com/categories/updates_{N}_d.html"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?college-uniform\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardStartRe = regexp.MustCompile(`<div class="update_details" data-setid="(\d+)"`)
	updateURLRe = regexp.MustCompile(`href="([^"]*/updates/[A-Za-z0-9_-]+\.html)"`)
	slugRe      = regexp.MustCompile(`/updates/([A-Za-z0-9_-]+)\.html`)
	// Title: the second `<a>` in the card (not the thumb-wrapping anchor —
	// that contains an `<img>`). We capture all text-anchor candidates and
	// pick the first one without an img child.
	textAnchorRe = regexp.MustCompile(`(?s)<a\s+href="[^"]*/updates/[^"]+"[^>]*>\s*([^<][^<]*?)\s*</a>`)
	thumbRe      = regexp.MustCompile(`src0_1x="([^"]+)"`)
	// Performers from `<span class="update_models">`.
	performerSectionRe = regexp.MustCompile(`(?s)<span class="update_models">(.*?)</span>`)
	performerAnchorRe  = regexp.MustCompile(`<a[^>]+href="[^"]*/models/[^"]+"[^>]*>([^<]+)</a>`)
	// Duration "21&nbsp;min&nbsp;of video".
	durationRe = regexp.MustCompile(`(\d+)\s*(?:&nbsp;|\s)+min\s*(?:&nbsp;|\s)+of\s*(?:&nbsp;|\s)*video`)
	// Date in `<div class="cell update_date">MM/DD/YYYY</div>`.
	dateRe    = regexp.MustCompile(`(?s)update_date">\s*(\d{2}/\d{2}/\d{4})`)
	maxPageRe = regexp.MustCompile(`updates_(\d+)_d\.html`)
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

func parseListing(body []byte) []sceneItem {
	page := string(body)
	starts := cardStartRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]sceneItem, 0, len(starts))
	seen := make(map[string]bool, len(starts))

	for i, loc := range starts {
		id := page[loc[2]:loc[3]]
		if seen[id] {
			continue
		}
		seen[id] = true

		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[loc[0]:end]

		item := sceneItem{id: id}
		if m := updateURLRe.FindStringSubmatch(block); m != nil {
			item.url = m[1]
			if slug := slugRe.FindStringSubmatch(item.url); slug != nil {
				// Slug-as-secondary-identifier; we prefer the numeric data-setid
				// since the slug here is the title and may contain accents in
				// other sites.
				_ = slug
			}
		}

		// Title: pick the first text-only anchor (skips the img-wrapping one).
		for _, m := range textAnchorRe.FindAllStringSubmatch(block, -1) {
			text := html.UnescapeString(strings.TrimSpace(m[1]))
			if text != "" && !strings.HasPrefix(text, "<") {
				item.title = text
				break
			}
		}

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			item.thumb = m[1]
		}

		if m := performerSectionRe.FindStringSubmatch(block); m != nil {
			for _, pm := range performerAnchorRe.FindAllStringSubmatch(m[1], -1) {
				name := html.UnescapeString(strings.TrimSpace(pm[1]))
				if name != "" {
					item.performers = append(item.performers, name)
				}
			}
		}

		if m := durationRe.FindStringSubmatch(block); m != nil {
			mins, _ := strconv.Atoi(m[1])
			item.duration = mins * 60
		}

		if m := dateRe.FindStringSubmatch(block); m != nil {
			if d, err := time.Parse("01/02/2006", m[1]); err == nil {
				item.date = d.UTC()
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

func (s *Scraper) listingURL(page int) string {
	return fmt.Sprintf("%s/categories/updates_%d_d.html", s.base, page)
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.Debugf(1, "collegeuniform: scraping full catalog")

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, "collegeuniform", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := estimateTotal(body, len(items))
		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = item.toScene(s.base, studioURL, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func (item sceneItem) toScene(base, studioURL string, now time.Time) models.Scene {
	url := item.url
	if strings.HasPrefix(url, "/") {
		url = base + url
	} else if url != "" && !strings.HasPrefix(url, "http") {
		url = base + "/" + url
	}
	thumb := item.thumb
	if thumb != "" && !strings.HasPrefix(thumb, "http") {
		thumb = base + "/" + strings.TrimLeft(thumb, "/")
	}
	return models.Scene{
		ID:         item.id,
		SiteID:     "collegeuniform",
		StudioURL:  studioURL,
		Title:      item.title,
		URL:        url,
		Thumbnail:  thumb,
		Date:       item.date,
		Duration:   item.duration,
		Performers: item.performers,
		Studio:     "College Uniform",
		ScrapedAt:  now,
	}
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
