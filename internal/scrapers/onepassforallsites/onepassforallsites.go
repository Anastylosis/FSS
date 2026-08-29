package onepassforallsites

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	hubBase      = "https://1passforallsites.com"
	siteID       = "1passforallsites"
	studioName   = "1 Pass for All Sites"
	defaultDelay = 500 * time.Millisecond
)

var childDomains = []string{
	"18virginsex.com",
	"amourbabes.com",
	"creampiedsweeties.com",
	"daddiesanddarlings.com",
	"dirtyass2mouth.com",
	"dirtydaddysgirls.com",
	"domywifeslut.com",
	"drilledmouths.com",
	"friskybabysitters.com",
	"hornythieftales.com",
	"hornyinhospital.com",
	"ifuckedherfinally.com",
	"lovelyteenland.com",
	"milfsonsticks.com",
	"mommiesdobunnies.com",
	"oldgoesyoung.com",
	"oldyounganal.com",
	"shabbyvirgins.com",
	"shemadeuslesbians.com",
	"spoiledvirgins.com",
	"straponservice.com",
	"trickyoldteacher.com",
	"wildyounghoneys.com",
	"younganaltryouts.com",
	"youngandbanged.com",
	"youngcumgulpers.com",
	"younglesbiansportal.com",
	"youngmodelscasting.com",
	"youngpornhomevideo.com",
}

var matchRe *regexp.Regexp

func init() {
	parts := []string{`1passforallsites\.com`}
	for _, d := range childDomains {
		parts = append(parts, regexp.QuoteMeta(d))
	}
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:` + strings.Join(parts, "|") + `)(?:/|$)`)
	scraper.Register(New())
}

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   hubBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"1passforallsites.com",
		"1passforallsites.com/episode/{id}/{slug}",
		"oldgoesyoung.com",
		"trickyoldteacher.com",
		"spoiledvirgins.com",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listItem struct {
	id        string
	slug      string
	title     string
	thumb     string
	performer string
	date      string
	series    string
	desc      string
	url       string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	delay := opts.Delay
	if delay == 0 {
		delay = defaultDelay
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listItem, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
				scene, err := s.fetchDetail(ctx, item, studioURL)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	base := s.resolveBase(studioURL)
	s.scrapePaginated(ctx, base, opts, out, work, delay)

	close(work)
	wg.Wait()
}

var childDomainRe = regexp.MustCompile(`^https?://(?:www\.)?([a-z0-9.-]+\.\w+)`)

func (s *Scraper) resolveBase(studioURL string) string {
	m := childDomainRe.FindStringSubmatch(studioURL)
	if m == nil {
		return s.base
	}
	domain := m[1]
	if domain == "1passforallsites.com" {
		return s.base
	}
	for _, d := range childDomains {
		if domain == d || domain == "www."+d {
			return "https://" + d
		}
	}
	return s.base
}

func (s *Scraper) scrapePaginated(ctx context.Context, base string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listItem, delay time.Duration) {
	totalSent := false
	isHub := base == s.base
	// The hub serves 38 cards a page and the child domains 18, so a hardcoded
	// page size read every child's first page as its last and truncated the
	// catalogue at 18 scenes. The first page's own count is the page size, and
	// the pager's highest page number is the end of the walk; a page that
	// yields nothing still stops the loop above.
	pageSize, lastPage := 0, 0
	prevPageKey := ""

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}

		if page > 1 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
		scraper.Debugf(1, "1passforallsites: fetching page %d", page)

		var pageURL string
		if isHub {
			pageURL = fmt.Sprintf("%s/scenes?page=%d&site=0", base, page)
		} else {
			pageURL = fmt.Sprintf("%s/?page=%d", base, page)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		items := parseListingCards(body, base)
		if len(items) == 0 {
			return
		}

		// A listing that ignores the page parameter echoes the same cards
		// forever. Comparing the whole page's ordered ids stops that without
		// depending on a page count the markup may not carry.
		key := pageKey(items)
		if key == prevPageKey {
			scraper.Debugf(1, "1passforallsites: page %d repeats the previous page, stopping", page)
			return
		}
		prevPageKey = key

		if !totalSent {
			pageSize = len(items)
			lastPage = parseTotalPages(body)
			total := lastPage * pageSize
			if total > 0 {
				scraper.Debugf(1, "1passforallsites: %d total scenes", total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
			totalSent = true
		}

		for _, item := range items {
			if opts.KnownIDs[item.id] {
				scraper.Debugf(1, "1passforallsites: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- item:
			case <-ctx.Done():
				return
			}
		}

		if lastPage > 0 && page >= lastPage {
			return
		}
		// With no pager to read, a short page is the only end signal left.
		if lastPage == 0 && pageSize > 0 && len(items) < pageSize {
			return
		}
	}
}

var (
	cardRe = regexp.MustCompile(
		`(?s)<a class="tn" href="(?:https?://[^"]*)?/episode/(\d+)/([^"]+)"` +
			`(?:\s+title="([^"]+)")?>` +
			`(.*?)</li>`,
	)
	thumbRe  = regexp.MustCompile(`(?:<img src='([^']+)'|<img src="([^"]+)")`)
	dateRe   = regexp.MustCompile(`(?:added </span>|Added:\s*<span>)([^<]+)`)
	modelsRe = regexp.MustCompile(`<p class="tn-models"><a[^>]*>([^<]+)`)
	perfRe   = regexp.MustCompile(`<p class="tn-title"><a[^>]*href="/model[^"]*"[^>]*>([^<]+)`)
	titleRe  = regexp.MustCompile(`<p class="tn-title"><a[^>]*(?:\s+title="([^"]+)")?[^>]*>([^<]+)`)
	descRe   = regexp.MustCompile(`<p class="tn-desc">([^<]+)`)
	sourceRe = regexp.MustCompile(`<p class="tn-source"><span>from: </span><a[^>]*>([^<]+)`)
)

func parseListingCards(body []byte, base string) []listItem {
	matches := cardRe.FindAllSubmatch(body, -1)
	seen := map[string]bool{}
	var items []listItem

	for _, m := range matches {
		id := string(m[1])
		if seen[id] {
			continue
		}
		seen[id] = true

		slug := string(m[2])
		rest := m[4]

		title := html.UnescapeString(string(m[3]))
		if title == "" {
			if tm := titleRe.FindSubmatch(rest); tm != nil {
				if len(tm[1]) > 0 {
					title = html.UnescapeString(string(tm[1]))
				} else {
					title = strings.TrimSuffix(strings.TrimSpace(html.UnescapeString(string(tm[2]))), "…")
				}
			}
		}

		item := listItem{
			id:    id,
			slug:  slug,
			title: title,
			url:   base + "/episode/" + id + "/" + slug,
		}

		if tm := thumbRe.FindSubmatch(rest); tm != nil {
			if len(tm[1]) > 0 {
				item.thumb = string(tm[1])
			} else {
				item.thumb = string(tm[2])
			}
		}
		if dm := dateRe.FindSubmatch(rest); dm != nil {
			item.date = strings.TrimSpace(string(dm[1]))
		}
		if pm := modelsRe.FindSubmatch(rest); pm != nil {
			item.performer = strings.TrimSpace(html.UnescapeString(string(pm[1])))
		} else if pm := perfRe.FindSubmatch(rest); pm != nil {
			item.performer = strings.TrimSpace(html.UnescapeString(string(pm[1])))
		}
		if dm := descRe.FindSubmatch(rest); dm != nil {
			item.desc = strings.TrimSpace(html.UnescapeString(string(dm[1])))
		}
		if sm := sourceRe.FindSubmatch(rest); sm != nil {
			item.series = strings.TrimSpace(string(sm[1]))
		}

		items = append(items, item)
	}
	return items
}

var maxPageRe = regexp.MustCompile(`page=(\d+)`)

// pageKey builds a stable signature from a page's ordered scene ids.
func pageKey(items []listItem) string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	return strings.Join(ids, "\x00")
}

func parseTotalPages(body []byte) int {
	maxPage := 0
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

var (
	detailDescRe  = regexp.MustCompile(`(?s)<h\d[^>]*>Description</h\d>\s*<p[^>]*>(.*?)</p>`)
	detailTagsRe  = regexp.MustCompile(`(?s)<p class="niches-list">(.*?)</p>`)
	detailTagRe   = regexp.MustCompile(`>([^<]+)</a>`)
	detailModelRe = regexp.MustCompile(`<p class="sp-info-name">.*?<a[^>]*>([^<]+)`)
	detailPathRe  = regexp.MustCompile(`(?s)<p class="path">(.*?)</p>`)
	detailLinkRe  = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
)

type detailData struct {
	description string
	tags        []string
	performer   string
	series      string
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := detailDescRe.FindSubmatch(body); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}

	if m := detailTagsRe.FindSubmatch(body); m != nil {
		for _, tm := range detailTagRe.FindAllSubmatch(m[1], -1) {
			tag := strings.TrimSpace(string(tm[1]))
			if tag != "" {
				d.tags = append(d.tags, tag)
			}
		}
	}

	if m := detailModelRe.FindSubmatch(body); m != nil {
		name := strings.TrimSpace(string(m[1]))
		if i := strings.Index(name, " <"); i >= 0 {
			name = name[:i]
		}
		d.performer = strings.TrimSpace(name)
	}

	if m := detailPathRe.FindSubmatch(body); m != nil {
		links := detailLinkRe.FindAllSubmatch(m[1], -1)
		for _, l := range links {
			name := strings.TrimSpace(string(l[1]))
			if name != "" && !strings.EqualFold(name, "Home") {
				d.series = name
				break
			}
		}
	}

	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, item listItem, studioURL string) (models.Scene, error) {
	body, err := s.fetchPage(ctx, item.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", item.id, err)
	}

	detail := parseDetailPage(body)

	description := detail.description
	if description == "" {
		description = item.desc
	}

	var performers []string
	perf := detail.performer
	if perf == "" {
		perf = item.performer
	}
	if perf != "" {
		performers = []string{perf}
	}

	series := detail.series
	if series == "" {
		series = item.series
	}

	var date time.Time
	if item.date != "" {
		if t, err := time.Parse("02 Jan 2006", item.date); err == nil {
			date = t.UTC()
		}
	}

	now := time.Now().UTC()
	return models.Scene{
		ID:          item.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         item.url,
		Date:        date,
		Description: description,
		Thumbnail:   item.thumb,
		Performers:  performers,
		Tags:        detail.tags,
		Studio:      studioName,
		Series:      series,
		Duration:    0,
		ScrapedAt:   now,
	}, nil
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
