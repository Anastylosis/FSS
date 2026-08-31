// Package mmpnetwork scrapes the MMP Network tours — the mmpnetwork.com hub
// plus the two brand sites that still serve the same template.
//
// Listing card at `/updates?p=N`:
//
//	<div class="scene">
//	  <figure><a href="/video/28/stupid-girl-creampie"><img src="https://free.povbitch.com/028pov/cover.jpg" /></a></figure>
//	  <div class="sceneInfo"><h3>Silly doll creampie</h3></div>
//	  <div class="sceneModels"><span>Featuring</span> <a href="/porn-tarja-king-28">Tarja King</a></div>
//	  <div class="sceneDate">Oct 10, 2019</div>
//	</div>
//
// The card's date is abbreviated and the hub leaves it blank altogether, so
// `/video/{id}/{slug}` is fetched for the full date, the runtime, the
// resolution ladder, the description and the tags.
//
// Four network brands are not covered: melonechallenge.com, myfirstpublic.com,
// shootourself.com, teenyplayground.com and wwmamm.com serve a newer tour with
// no `scene` cards, and takevan.com 404s its `/updates`. Their scenes are
// reachable through the hub, which lists the whole network.
package mmpnetwork

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

// SiteConfig describes one MMP Network site.
type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

var sites = []SiteConfig{
	{SiteID: "mmpnetwork", Domain: "mmpnetwork.com", StudioName: "MMP Network"},
	{SiteID: "povbitch", Domain: "povbitch.com", StudioName: "PovBitch"},
	{SiteID: "fakeshooting", Domain: "fakeshooting.com", StudioName: "Fake Shooting"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

// newFor builds the scraper for a site id. Used by tests.
func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return New(cfg)
		}
	}
	return nil
}

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		base:    "https://" + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{s.cfg.Domain, s.cfg.Domain + "/updates"}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: scraping full catalogue", s.cfg.SiteID)
	items, ok := s.collectListing(ctx, opts, out)
	if !ok || ctx.Err() != nil {
		return
	}
	s.fetchDetails(ctx, studioURL, items, opts, out)
}

func (s *Scraper) collectListing(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) (items []listItem, ok bool) {
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

		pageURL := fmt.Sprintf("%s/updates?p=%d", s.base, page)
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

		// The pager names its own last page; a page that adds nothing new is
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
				body, err := s.fetchPage(ctx, s.base+item.path)
				if err != nil {
					// The card names the scene; a detail page that will not
					// load costs the full date, runtime, resolution,
					// description and tags.
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.path, err)):
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
	path        string
	title       string
	thumbnail   string
	performers  []string
	date        string
	duration    int
	resolution  string
	description string
	tags        []string
}

var (
	cardStartRe = regexp.MustCompile(`<div class="scene">`)
	// The scene path carries the numeric id the site keys on; the slug is
	// derived from the title. Fake Shooting writes protocol-relative hrefs
	// (`//fakeshooting.com/video/21/…`) where the others write a bare path, so
	// the host is optional and only the path is captured.
	cardLinkRe  = regexp.MustCompile(`href="(?:https?:)?(?://[^/"]+)?(/video/(\d+)/[^"]*)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<div class="sceneInfo[^"]*">\s*<h3>(.*?)</h3>`)
	cardThumbRe = regexp.MustCompile(`<img[^>]+src="(https?://[^"?\s]+)`)
	cardModelRe = regexp.MustCompile(`(?s)<div class="sceneModels"[^>]*>(.*?)</div>`)
	pagerRe     = regexp.MustCompile(`(?s)<div class="pagination">(.*?)</div>`)
	pagerNumRe  = regexp.MustCompile(`[?&]p=(\d+)`)

	detailTitleRe = regexp.MustCompile(`(?s)<h2 class="videoTitle">(.*?)</h2>`)
	detailDateRe  = regexp.MustCompile(`(?s)<div class="videoDate">\s*([A-Z][a-z]+ \d{1,2}, \d{4})`)
	detailModelRe = regexp.MustCompile(`(?s)<div class="videoDate">(.*?)</div>`)
	detailDescRe  = regexp.MustCompile(`(?s)<div class="videoDescription">(.*?)</div>`)
	detailTagsRe  = regexp.MustCompile(`(?s)<div class="videoTags">(.*?)</div>`)
	detailLenRe   = regexp.MustCompile(`(?s)<div class="videoLength">.*?</span>\s*([\d:]+)`)
	detailQualRe  = regexp.MustCompile(`(?s)<div class="videoQuality">.*?</span>\s*([^<]+)`)
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

		m := cardLinkRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		item := listItem{id: string(m[2]), path: html.UnescapeString(string(m[1]))}
		if t := cardTitleRe.FindSubmatch(block); t != nil {
			item.title = cleanText(string(t[1]))
		}
		if t := cardThumbRe.FindSubmatch(block); t != nil {
			item.thumbnail = html.UnescapeString(string(t[1]))
		}
		if t := cardModelRe.FindSubmatch(block); t != nil {
			item.performers = anchorNames(t[1])
		}
		if item.title == "" {
			continue
		}
		items = append(items, item)
	}
	return items
}

func enrichFromDetail(body []byte, item *listItem) {
	if m := detailTitleRe.FindSubmatch(body); m != nil {
		if t := cleanText(string(m[1])); t != "" {
			item.title = t
		}
	}
	// The card abbreviates the date and the hub leaves it out entirely; the
	// detail page spells it in full.
	if m := detailDateRe.FindSubmatch(body); m != nil {
		item.date = cleanText(string(m[1]))
	}
	if m := detailModelRe.FindSubmatch(body); m != nil {
		if names := anchorNames(m[1]); len(names) > 0 {
			item.performers = names
		}
	}
	if m := detailDescRe.FindSubmatch(body); m != nil {
		item.description = cleanText(tagStripRe.ReplaceAllString(string(m[1]), " "))
	}
	if m := detailTagsRe.FindSubmatch(body); m != nil {
		item.tags = anchorNames(m[1])
	}
	if m := detailLenRe.FindSubmatch(body); m != nil {
		item.duration = parseutil.ParseDurationColon(cleanText(string(m[1])))
	}
	if m := detailQualRe.FindSubmatch(body); m != nil {
		item.resolution = topResolution(cleanText(string(m[1])))
	}
}

// topResolution reads the highest rendition from the "1080p, 720p, 480p"
// ladder the detail page prints.
func topResolution(raw string) string {
	best := 0
	for _, m := range regexp.MustCompile(`(\d+)p`).FindAllStringSubmatch(raw, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > best {
			best = n
		}
	}
	if best == 0 {
		return ""
	}
	return strconv.Itoa(best) + "p"
}

func maxPage(body []byte) int {
	pm := pagerRe.FindSubmatch(body)
	if pm == nil {
		return 0
	}
	last := 0
	for _, m := range pagerNumRe.FindAllSubmatch(pm[1], -1) {
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
		URL:         s.base + item.path,
		Description: item.description,
		Thumbnail:   item.thumbnail,
		Performers:  item.performers,
		Tags:        item.tags,
		Studio:      s.cfg.StudioName,
		Duration:    item.duration,
		Resolution:  item.resolution,
		ScrapedAt:   now,
	}
	if d, err := parseutil.TryParseDate(item.date, "January 2, 2006", "Jan 2, 2006"); err == nil {
		sc.Date = d
	}
	return sc
}
