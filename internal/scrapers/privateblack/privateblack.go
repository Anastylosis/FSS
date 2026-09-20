// Package privateblack scrapes privateblack.com — the Private network's
// interracial sub-brand running an older variant of the Private CMS.
//
// Why this isn't covered by the `private` package: privateblack.com uses
// the same outer "scene" container but ships a different card layout
// (Bootstrap grid `<li class=" col-md-3 …">` instead of `<li class="card">`,
// no `data-track="TITLE_LINK"` / `PORNSTAR_LINK"` attributes, plain `<h3>`
// + `<ul class="scene-models">`). The differences are small but spread
// across enough field-extraction regexes that splitting parsers is cleaner
// than branching the existing one.
//
// Card markup (one card):
//
//	<li class=" col-md-3 col-xs-6 col-sm-6 col-xs-12">
//	  <div class="scene">
//	    <a href="https://www.privateblack.com/scene/{slug}/{id}" title="…">
//	      <picture>…<img srcset="https://pblack77.st-content.com/…/contentthumbs/{n}.jpg…"></picture>
//	    </a>
//	    <ul class="scene-details">
//	      <li class="hdlabel"><span>HD</span></li>      ← or "ultrahdlabel" / "4K"
//	    </ul>
//	    <div>
//	      <h3><a href="https://www.privateblack.com/scene/{slug}/{id}">Title</a></h3>
//	      <ul class="scene-models">
//	        <li><a href="https://www.privateblack.com/pornstar/{n}-{slug}/">Name</a></li>
//	      </ul>
//	      <div class="scene-votes"> … </div>
//	      <span class="scene-date">05/25/2026</span>
//	    </div>
//	  </div>
//	</li>
//
// Pagination: `/scenes/{N}/` (path-based, trailing slash). Past-end pages
// return zero `<div class="scene">` cards (clean stop signal). The default
// sort is newest-first so `KnownIDs` early-stop works.
//
// Detail pages are public on this domain but we don't fetch them — the
// listing card carries title, performers, date and thumb, which is
// everything Stash needs to match scenes.
package privateblack

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

const (
	defaultBase = "https://www.privateblack.com"
	scraperID   = "privateblack"
	studioName  = "Private"
	seriesName  = "Private Black"
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

func (s *Scraper) ID() string { return scraperID }
func (s *Scraper) Patterns() []string {
	return []string{
		"privateblack.com/",
		"privateblack.com/scenes",
		"privateblack.com/scenes/{N}/",
		"privateblack.com/pornstar/{id}-{slug}/",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?privateblack\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// `<div class="scene">` opens each card; the surrounding `<li class=" col-md-3 …">`
	// is bootstrap chrome that we don't need to match against.
	cardStartRe = regexp.MustCompile(`<div class="scene">`)
	// Scene URL — `/scene/{slug}/{id}` on privateblack.com. The numeric ID
	// at the end is the stable scene identifier.
	sceneURLRe = regexp.MustCompile(
		`href="(https?://(?:www\.)?privateblack\.com/scene/[^"]+/(\d+))"`,
	)
	// Title — the `<h3><a href="…">Title</a></h3>` text. Constraining the
	// capture group to `[^<]+` skips the thumb-wrapping `<a>` whose content
	// starts with `<picture>`.
	titleH3Re = regexp.MustCompile(`(?s)<h3>\s*<a[^>]+href="[^"]+/scene/[^"]+/\d+"[^>]*>\s*([^<]+?)\s*</a>\s*</h3>`)
	// Performer link — `<a href="/pornstar/{N}-{slug}/">Name</a>` inside
	// `<ul class="scene-models">`.
	performerRe = regexp.MustCompile(
		`<a[^>]+href="[^"]*/pornstar/\d+-[^"]+"[^>]*>\s*([^<]+?)\s*</a>`,
	)
	// Date — `<span class="scene-date">MM/DD/YYYY</span>`.
	dateRe = regexp.MustCompile(`<span class="scene-date">\s*(\d{2}/\d{2}/\d{4})\s*</span>`)
	// Thumbnail — prefer the `<img srcset="…">` URL (the fallback the browser
	// would actually use after picking from `<picture>`), not the `<source>`
	// srcsets which are responsive variants. The `<img>` srcset usually
	// lists multiple comma-separated URLs by viewport size; we take the
	// first. As a fallback (when srcset is absent), we read the `src`.
	imgSrcsetRe = regexp.MustCompile(`<img[^>]+srcset="(https?://pblack77\.st-content\.com/[^"\s,]+)`)
	imgSrcRe    = regexp.MustCompile(`<img[^>]+src="(https?://pblack77\.st-content\.com/[^"]+)"`)
	// Largest /scenes/{N}/ page number anywhere in the page (covers the
	// "Skip to page" helper that lists 18, 50, 100, etc.).
	pageLinkRe = regexp.MustCompile(`href="[^"]*/scenes/(\d+)/?"`)
)

type sceneItem struct {
	id         string
	url        string
	title      string
	performers []string
	date       time.Time
	thumb      string
}

func parseListing(body []byte) []sceneItem {
	page := string(body)
	starts := cardStartRe.FindAllStringIndex(page, -1)
	items := make([]sceneItem, 0, len(starts))
	seen := make(map[string]bool, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[loc[0]:end]

		var item sceneItem
		if m := sceneURLRe.FindStringSubmatch(block); m != nil {
			item.url = m[1]
			item.id = m[2]
		}
		if item.id == "" || seen[item.id] {
			continue
		}
		seen[item.id] = true

		if m := titleH3Re.FindStringSubmatch(block); m != nil {
			item.title = html.UnescapeString(strings.TrimSpace(m[1]))
		}
		for _, pm := range performerRe.FindAllStringSubmatch(block, -1) {
			name := html.UnescapeString(strings.TrimSpace(pm[1]))
			if name != "" {
				item.performers = append(item.performers, name)
			}
		}
		item.performers = dedupStrings(item.performers)
		if m := dateRe.FindStringSubmatch(block); m != nil {
			if d, err := time.Parse("01/02/2006", m[1]); err == nil {
				item.date = d.UTC()
			}
		}
		// Prefer the `<img srcset>` URL; fall back to plain src.
		if m := imgSrcsetRe.FindStringSubmatch(block); m != nil {
			item.thumb = m[1]
		} else if m := imgSrcRe.FindStringSubmatch(block); m != nil {
			item.thumb = m[1]
		}

		items = append(items, item)
	}
	return items
}

func estimateTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range pageLinkRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

func (s *Scraper) listingURL(page int) string {
	if page <= 1 {
		return s.base + "/scenes"
	}
	return fmt.Sprintf("%s/scenes/%d/", s.base, page)
}

var pornstarPathRe = regexp.MustCompile(`/pornstar/(\d+-[A-Za-z0-9-]+)`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if pornstarPathRe.MatchString(studioURL) {
		scraper.Debugf(1, "privateblack: detected pornstar page")
		s.scrapePornstarPage(ctx, studioURL, opts, out)
		return
	}

	scraper.Debugf(1, "privateblack: scraping full catalog")
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, "privateblack", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
		if page == 1 && len(items) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first listing page"))
		}
		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = s.toScene(item, studioURL, now)
		}
		return scraper.PageResult{
			Scenes: scenes,
			Total:  estimateTotal(body, len(items)),
		}, nil
	})
}

func (s *Scraper) scrapePornstarPage(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	pageURL := studioURL
	if !strings.HasPrefix(pageURL, "http") {
		pageURL = s.base + pageURL
	}

	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	items := parseListing(body)
	if len(items) == 0 {
		select {
		case out <- scraper.Error(scraper.ParseError(pageURL, fmt.Errorf("no scene cards on pornstar page"))):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "privateblack: found %d scenes on pornstar page", len(items))

	now := time.Now().UTC()
	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	for _, item := range items {
		if opts.KnownIDs[item.id] {
			scraper.Debugf(1, "privateblack: hit known ID %s, stopping early", item.id)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(s.toScene(item, studioURL, now)):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) toScene(item sceneItem, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:         item.id,
		SiteID:     scraperID,
		StudioURL:  studioURL,
		Title:      item.title,
		URL:        item.url,
		Thumbnail:  item.thumb,
		Date:       item.date,
		Performers: item.performers,
		Studio:     studioName,
		Series:     seriesName,
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

func dedupStrings(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
