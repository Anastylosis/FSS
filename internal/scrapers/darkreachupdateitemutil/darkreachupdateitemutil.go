// Package darkreachupdateitemutil scrapes Darkreach Communications sites
// running the "updateItem" template family (spartavideo, watchyoujerk,
// angelasommers). All three share a `<div class="updateItem">` card wrapper
// and `/updates/{slug}.html` detail URLs, but the inner field layout varies:
//
//   - spartavideo:    `updateInfo` block with h5 title; no date/duration/models.
//   - watchyoujerk:   `updateInfo` block with h5 title + `tour_update_models`
//     performers (date is HTML-commented-out).
//   - angelasommers:  `updateDetails` block with h4 title + duration
//     ("10&nbsp;min") + date ("MM/DD/YYYY").
//
// The util extracts whatever's present per card and leaves the rest blank.
// Listing pagination form: `{base}/categories/updates_{N}_d.html`.
package darkreachupdateitemutil

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

type SiteConfig struct {
	ID       string
	SiteBase string // "https://spartavideo.com" — no trailing slash
	Studio   string
	// TourPrefix is "/tour" for sites whose paths are rooted at /tour/
	// (hungarianhoneys), or "" for sites at the bare path (spartavideo).
	TourPrefix string
	// DetailPathSegment is the path component for scene-detail URLs — most
	// sites use "/updates" (default) but some (terrorxxx) use "/trailers".
	DetailPathSegment string
	// ListingBase is the listing-URL stem WITHOUT the page-number suffix or
	// extension — default is "/categories/updates" (yielding
	// `/categories/updates_{N}_d.html`). Sites with a different base
	// (e.g. terrorxxx's `/categories/Movies`) override this.
	ListingBase string
	Patterns    []string
	MatchRe     *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	if cfg.DetailPathSegment == "" {
		cfg.DetailPathSegment = "/updates"
	}
	if cfg.ListingBase == "" {
		cfg.ListingBase = "/categories/updates"
	}
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.ID }
func (s *Scraper) Patterns() []string {
	domain := strings.TrimPrefix(strings.TrimPrefix(s.cfg.SiteBase, "https://"), "http://")
	return append(s.cfg.Patterns, domain+s.cfg.TourPrefix+"/models/{slug}.html")
}
func (s *Scraper) MatchesURL(u string) bool {
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardStartRe = regexp.MustCompile(`<div class="updateItem">`)
	// Anchor inside the card pointing at the detail page; first match wins.
	// Accepts both /updates/{slug}.html and /trailers/{slug}.html so this
	// util can also handle terrorxxx-style sites.
	updateURLRe = regexp.MustCompile(`<a[^>]+href="([^"]*/(?:updates|trailers)/[A-Za-z0-9_-]+\.html)"`)
	// Title is the text content of an <h4> or <h5> link wrapping the same URL.
	titleH4Re = regexp.MustCompile(`(?s)<h4>\s*<a[^>]+href="[^"]*/(?:updates|trailers)/[^"]+"[^>]*>\s*([^<]+?)\s*</a>`)
	titleH5Re = regexp.MustCompile(`(?s)<h5>\s*<a[^>]+href="[^"]*/(?:updates|trailers)/[^"]+"[^>]*>\s*([^<]+?)\s*</a>`)
	thumbRe   = regexp.MustCompile(`src0_1x="([^"]+)"`)
	slugRe    = regexp.MustCompile(`/(?:updates|trailers)/([A-Za-z0-9_-]+)\.html`)
	// Performers from `<span class="tour_update_models">`.
	performerSectionRe = regexp.MustCompile(`(?s)<span class="tour_update_models">(.*?)</span>`)
	performerAnchorRe  = regexp.MustCompile(`<a[^>]+href="[^"]*/models/[^"]+"[^>]*>([^<]+)</a>`)
	// Duration "10&nbsp;min" or "10 min". &nbsp; appears literally in the HTML.
	durationMinsRe = regexp.MustCompile(`(\d+)\s*(?:&nbsp;|\s)*min(?:&nbsp;|\s|<|$)`)
	// Date "08/25/2022".
	dateRe    = regexp.MustCompile(`(\d{2}/\d{2}/\d{4})`)
	dateDotRe = regexp.MustCompile(`(\d{2}\.\d{2}\.\d{4})`)
	// Pagination max-page extraction. Accepts `updates_N_d.html` (default)
	// and `Movies_N_d.html` (terrorxxx — capital M).
	maxPageRe = regexp.MustCompile(`(?:updates|[Mm]ovies)_(\d+)_d\.html`)
)

type sceneItem struct {
	id         string
	title      string
	url        string
	thumb      string
	date       time.Time
	duration   int // seconds
	performers []string
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
		if m := updateURLRe.FindStringSubmatch(block); m != nil {
			item.url = m[1]
			if slug := slugRe.FindStringSubmatch(item.url); slug != nil {
				item.id = slug[1]
			}
		}
		if item.id == "" || seen[item.id] {
			continue
		}
		seen[item.id] = true

		// Title from h4 or h5 — different sites use different headings.
		if m := titleH4Re.FindStringSubmatch(block); m != nil {
			item.title = html.UnescapeString(strings.TrimSpace(m[1]))
		} else if m := titleH5Re.FindStringSubmatch(block); m != nil {
			item.title = html.UnescapeString(strings.TrimSpace(m[1]))
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

		if m := durationMinsRe.FindStringSubmatch(block); m != nil {
			mins, _ := strconv.Atoi(m[1])
			item.duration = mins * 60
		}

		// Date: MM/DD/YYYY. Skip placeholder dates of all zeros.
		for _, m := range dateRe.FindAllStringSubmatch(block, -1) {
			if d, err := time.Parse("01/02/2006", m[1]); err == nil {
				item.date = d.UTC()
				break
			}
		}
		// Some sites (hammerboys) render dates as DD.MM.YYYY instead.
		if item.date.IsZero() {
			for _, m := range dateDotRe.FindAllStringSubmatch(block, -1) {
				if d, err := time.Parse("02.01.2006", m[1]); err == nil {
					item.date = d.UTC()
					break
				}
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
	return fmt.Sprintf("%s%s%s_%d_d.html", s.cfg.SiteBase, s.cfg.TourPrefix, s.cfg.ListingBase, page)
}

var modelSlugRe = regexp.MustCompile(`/models/([^_/.]+?)(?:\.html)?$`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if modelSlugRe.MatchString(studioURL) {
		scraper.Debugf(1, "%s: detected model page", s.cfg.ID)
		s.scrapeModelPage(ctx, studioURL, opts, out)
		return
	}

	scraper.Debugf(1, "%s: scraping full catalog", s.cfg.ID)
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
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
			scenes[i] = item.toScene(s.cfg.ID, s.cfg.SiteBase, studioURL, s.cfg.Studio, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func (s *Scraper) scrapeModelPage(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	pageURL := studioURL
	if !strings.HasPrefix(pageURL, "http") {
		pageURL = s.cfg.SiteBase + pageURL
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
		// Not every site on this template renders its model pages as
		// updateItem cards — bigbootytgirls.com uses an `update_table_right`
		// block instead. Returning quietly there makes a model URL look like a
		// performer with no scenes, so say what happened.
		select {
		case out <- scraper.Error(scraper.ParseError(pageURL, fmt.Errorf("no updateItem cards on model page"))):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: found %d scenes on model page", s.cfg.ID, len(items))

	now := time.Now().UTC()
	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	for _, item := range items {
		if opts.KnownIDs[item.id] {
			scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.ID, item.id)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(item.toScene(s.cfg.ID, s.cfg.SiteBase, studioURL, s.cfg.Studio, now)):
		case <-ctx.Done():
			return
		}
	}
}

func (item sceneItem) toScene(siteID, siteBase, studioURL, studio string, now time.Time) models.Scene {
	url := item.url
	if strings.HasPrefix(url, "/") {
		url = siteBase + url
	}
	thumb := item.thumb
	if thumb != "" && !strings.HasPrefix(thumb, "http") {
		thumb = siteBase + "/" + strings.TrimLeft(thumb, "/")
	}
	return models.Scene{
		ID:         item.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      item.title,
		URL:        url,
		Thumbnail:  thumb,
		Date:       item.date,
		Duration:   item.duration,
		Performers: item.performers,
		Studio:     studio,
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
