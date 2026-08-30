// Package paysitemanagerutil scrapes sites running the PaySiteManager tour.
//
// Listing card:
//
//	<div class=" videoBlock">
//	  <div class="videoPic"><a href="https://site/updates/{slug}">
//	    <img src="https://site/content/thumbs/40593/preview-09.jpg" /></a></div>
//	  <h3><a href="…/updates/{slug}">Armpit Worship</a></h3>
//	  <div class="modelName"><a href="…/models/x">Daphne Brooks</a>, <a …>Eve Tyler</a></div>
//	  <ul class="contentInfo">
//	    <li><i class="fa-solid fa-clock"></i>06:54</li>
//	    <li><i class="fas fa-calendar"></i>Mar 14, 2026</li>
//	    <li … data-price="$7.99">…</li>
//	  </ul>
//	</div>
//
// The card's title is truncated on some sites ("Straitjacketed Lil Missy UK
// Unwittingly Committed to In..."), so the detail page is fetched for the full
// title, the description and the tag list.
//
// The scene id is the numeric content id in the thumbnail path
// (`/content/thumbs/40593/`), which is also the id the cart uses. The slug is
// derived from the title and would change with it.
package paysitemanagerutil

import (
	"context"
	"fmt"
	"html"
	"net/http"
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

// SiteConfig describes one PaySiteManager site.
type SiteConfig struct {
	SiteID     string
	SiteBase   string // e.g. "https://thesensitivespot.com" — no trailing slash
	StudioName string
	// ListPath is the catalogue path, with a leading slash. Empty means
	// "/updates".
	ListPath string
}

func (c SiteConfig) listPath() string {
	if c.ListPath == "" {
		return "/updates"
	}
	return c.ListPath
}

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

// New builds a registration-ready scraper for one site.
func New(cfg SiteConfig) *Scraper {
	host := strings.TrimPrefix(strings.TrimPrefix(cfg.SiteBase, "https://"), "http://")
	host = strings.TrimPrefix(host, "www.")
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		base:    cfg.SiteBase,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(host) + `(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	host := strings.TrimPrefix(strings.TrimPrefix(s.cfg.SiteBase, "https://"), "http://")
	return []string{
		host,
		host + s.cfg.listPath(),
		host + "/tags/{tag}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

// tagRe recognises a tag listing, which paginates the same way the catalogue
// does.
var tagRe = regexp.MustCompile(`/tags/([^/?#]+)`)

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	listPath := s.cfg.listPath()
	if m := tagRe.FindStringSubmatch(studioURL); m != nil {
		listPath = "/tags/" + m[1]
		scraper.Debugf(1, "%s: scraping tag %q", s.cfg.SiteID, m[1])
	} else {
		scraper.Debugf(1, "%s: scraping full catalogue", s.cfg.SiteID)
	}

	items, ok := s.collectListing(ctx, listPath, opts, out)
	if !ok || ctx.Err() != nil {
		return
	}
	s.fetchDetails(ctx, studioURL, items, opts, out)
}

func (s *Scraper) collectListing(ctx context.Context, listPath string, opts scraper.ListOpts, out chan<- scraper.SceneResult) (items []listItem, ok bool) {
	seen := make(map[string]bool)
	sentTotal := false
	lastPage := 0

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return items, false
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return items, false
			}
		}

		pageURL := fmt.Sprintf("%s%s?page=%d", s.base, listPath, page)
		scraper.Debugf(1, "%s: fetching listing page %d (%s)", s.cfg.SiteID, page, pageURL)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return items, false
		}

		parsed := parseListing(body)
		if len(parsed) == 0 {
			return items, true
		}

		if !sentTotal {
			lastPage = maxPage(body)
			if lastPage > 0 {
				select {
				case out <- scraper.Progress(lastPage * len(parsed)):
				case <-ctx.Done():
					return items, false
				}
				sentTotal = true
			}
		}

		fresh := 0
		for _, item := range parsed {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			if opts.KnownIDs[item.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.SiteID, item.id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return items, true
			}
			fresh++
			items = append(items, item)
		}

		// The pager names its own last page; a page that added nothing new is
		// the fallback for a listing that prints none.
		if fresh == 0 || (lastPage > 0 && page >= lastPage) {
			return items, true
		}
	}
}

func (s *Scraper) fetchDetails(ctx context.Context, studioURL string, items []listItem, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(items) == 0 {
		return
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)

	work := make(chan listItem, workers)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer close(work)

	now := time.Now().UTC()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				body, err := s.fetchPage(ctx, item.url)
				if err != nil {
					// The card names the scene; a detail page that will not
					// load costs the full title, description and tags.
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.url, err)):
					case <-ctx.Done():
						return
					}
				} else {
					enrichFromDetail(body, &item)
				}
				select {
				case out <- scraper.Scene(s.toScene(item, studioURL, now)):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, item := range items {
		select {
		case work <- item:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- parsing ----

type listItem struct {
	id          string
	title       string
	url         string
	thumbnail   string
	performers  []string
	date        string
	duration    int
	price       float64
	description string
	tags        []string
}

var (
	cardStartRe = regexp.MustCompile(`<div class="\s*videoBlock">`)
	cardURLRe   = regexp.MustCompile(`<a href="([^"]+/(?:updates|scenes)/[^"]+)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<h3>\s*<a[^>]*>(.*?)</a>`)
	thumbRe     = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	modelsRe    = regexp.MustCompile(`(?s)<div class="modelName">(.*?)</div>`)
	durationRe  = regexp.MustCompile(`fa-clock"></i>\s*([\d:]+)`)
	dateRe      = regexp.MustCompile(`fa-calendar"></i>\s*([A-Z][a-z]{2} \d{1,2}, \d{4})`)
	priceRe     = regexp.MustCompile(`data-price="\$?([\d.]+)"`)
	// contentIDRe reads the numeric content id out of a thumbnail path.
	contentIDRe = regexp.MustCompile(`/content/thumbs/(\d+)/`)
	pageNumRe   = regexp.MustCompile(`[?&]page=(\d+)`)
	pagerRe     = regexp.MustCompile(`(?s)<[^>]+class="pagination"[^>]*>(.*?)</div>`)

	detailTitleRe = regexp.MustCompile(`(?s)<h1>(.*?)</h1>`)
	detailDescRe  = regexp.MustCompile(`(?s)<div class="\s*videoDescription\s*">.*?<p>(.*?)</p>`)
	detailTagsRe  = regexp.MustCompile(`(?s)<div class="tags">(.*?)</ul>`)
	anchorRe      = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	tagStripRe    = regexp.MustCompile(`<[^>]*>`)
)

func parseListing(body []byte) []listItem {
	locs := cardStartRe.FindAllIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		var item listItem
		m := cardURLRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		item.url = html.UnescapeString(string(m[1]))
		if t := cardTitleRe.FindSubmatch(block); t != nil {
			item.title = cleanText(string(t[1]))
		}
		if t := thumbRe.FindSubmatch(block); t != nil {
			item.thumbnail = html.UnescapeString(string(t[1]))
		}
		item.id = contentID(item.thumbnail, item.url)
		if item.id == "" || item.title == "" {
			continue
		}
		if mm := modelsRe.FindSubmatch(block); mm != nil {
			item.performers = anchorNames(mm[1])
		}
		if dm := durationRe.FindSubmatch(block); dm != nil {
			item.duration = parseutil.ParseDurationColon(string(dm[1]))
		}
		if dm := dateRe.FindSubmatch(block); dm != nil {
			item.date = string(dm[1])
		}
		if pm := priceRe.FindSubmatch(block); pm != nil {
			item.price, _ = strconv.ParseFloat(string(pm[1]), 64)
		}
		items = append(items, item)
	}
	return items
}

// contentID prefers the numeric id in the thumbnail path; a card whose
// thumbnail is missing falls back to the URL slug so the scene is still
// collected.
func contentID(thumb, sceneURL string) string {
	if m := contentIDRe.FindStringSubmatch(thumb); m != nil {
		return m[1]
	}
	slug := strings.TrimSuffix(sceneURL, "/")
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		slug = slug[i+1:]
	}
	return slug
}

func enrichFromDetail(body []byte, item *listItem) {
	// The card truncates a long title; the detail page's h1 carries it whole.
	if m := detailTitleRe.FindSubmatch(body); m != nil {
		if t := cleanText(string(m[1])); t != "" {
			item.title = t
		}
	}
	if m := detailDescRe.FindSubmatch(body); m != nil {
		item.description = cleanText(tagStripRe.ReplaceAllString(strings.ReplaceAll(string(m[1]), "<br>", " "), " "))
	}
	if m := detailTagsRe.FindSubmatch(body); m != nil {
		item.tags = anchorNames(m[1])
	}
}

// maxPage reads the highest page the pager names.
func maxPage(body []byte) int {
	pm := pagerRe.FindSubmatch(body)
	if pm == nil {
		return 0
	}
	last := 0
	for _, m := range pageNumRe.FindAllSubmatch(pm[1], -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > last {
			last = n
		}
	}
	return last
}

func anchorNames(block []byte) []string {
	var names []string
	seen := make(map[string]bool)
	for _, m := range anchorRe.FindAllSubmatch(block, -1) {
		n := cleanText(tagStripRe.ReplaceAllString(string(m[1]), " "))
		if n == "" || seen[strings.ToLower(n)] {
			continue
		}
		seen[strings.ToLower(n)] = true
		names = append(names, n)
	}
	return names
}

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (s *Scraper) toScene(item listItem, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          item.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         item.url,
		Description: item.description,
		Thumbnail:   item.thumbnail,
		Performers:  item.performers,
		Tags:        item.tags,
		Studio:      s.cfg.StudioName,
		Duration:    item.duration,
		ScrapedAt:   now,
	}
	if d, err := parseutil.TryParseDate(item.date, "Jan 2, 2006", "Jan 02, 2006"); err == nil {
		sc.Date = d
	}
	if item.price > 0 {
		sc.AddPrice(models.PriceSnapshot{Date: now, Regular: item.price})
	}
	return sc
}
