package tainster

import (
	"context"
	"errors"
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
	"github.com/Anastylosis/FSS/scraper"
)

const defaultBase = "https://www.sinx.com"

type Scraper struct {
	Client  *http.Client
	baseURL string
}

func New() *Scraper {
	return &Scraper{
		Client:  httpx.NewClient(30 * time.Second),
		baseURL: defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "tainster" }

func (s *Scraper) Patterns() []string {
	return []string{
		"sinx.com/videos/all",
		"sinx.com/{channel}",
		"sinx.com/channel/{series}/all",
		"sinx.com/girls/{id}-{slug}",
		"sinx.com/tag/{id}-{slug}",
		"tainster.com",
		"partyhardcore.com",
		"fullyclothedsex.com",
		"pissinginaction.com",
		"slimewave.com",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:` +
	`sinx\.com|tainster\.com|` +
	`partyhardcore\.com|swingingpornstars\.com|cumsquad\.com|` +
	`peesquad\.com|allwam\.net|fullyclothedpissing\.com|` +
	`fullyclothedsex\.com|my-fetish\.net|pissinginaction\.com|` +
	`madsexparty\.com|tyrannized\.com|slimewave\.com|` +
	`orgasmatics\.com|pornstarsathome\.com|leonyaprill\.com|` +
	`eromaxx\.net|messy-wrestling\.com|guysgocrazy\.com|` +
	`cumonjugs\.com|orgymax\.com|erostreaming\.com` +
	`)(?:/|$)`)

var (
	girlsRe      = regexp.MustCompile(`/girls/(\d+-[\w-]+)`)
	tagRe        = regexp.MustCompile(`/tag/(\d+-[\w-]+)`)
	videosRe     = regexp.MustCompile(`/videos/(all|sale)`)
	channelAllRe = regexp.MustCompile(`/channel/([\w-]+)/all`)
)

type channelInfo struct {
	slug     string
	isSeries bool
}

// subsiteDomains maps subsite domains to their sinx.com channel slug.
// Slugs verified against live sinx.com channel pages.
var subsiteDomains = map[string]channelInfo{
	"partyhardcore.com":       {"Party-Hardcore", true},
	"cumsquad.com":            {"Cumsquad", false},
	"peesquad.com":            {"Peesquad", false},
	"allwam.net":              {"Allwam", false},
	"fullyclothedpissing.com": {"Fullyclothed-Pissing", false},
	"fullyclothedsex.com":     {"Fullyclothed-Sex", false},
	"my-fetish.net":           {"My-Fetish", false},
	"pissinginaction.com":     {"Pissing-In-Action", false},
	"madsexparty.com":         {"Mad-Sex-Party", true},
	"tyrannized.com":          {"Tyrannized", false},
	"slimewave.com":           {"Slime-Wave", false},
	"orgasmatics.com":         {"Orgasmatics", false},
	"pornstarsathome.com":     {"Pornstars-At-Home", false},
	"leonyaprill.com":         {"Leony-Aprill", false},
	"messy-wrestling.com":     {"Messy-Wrestling", false},
	"guysgocrazy.com":         {"Guys-Go-Crazy", false},
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	videoPaths := s.resolveVideoPaths(ctx, studioURL, opts, out)
	if len(videoPaths) == 0 {
		return
	}

	var items []listingItem
	for _, vp := range videoPaths {
		if ctx.Err() != nil {
			return
		}
		pageItems := s.collectItems(ctx, vp, opts, out)
		items = append(items, pageItems...)
	}

	if len(items) == 0 {
		return
	}
	scraper.Debugf(1, "tainster: collected %d items from listing, fetching details", len(items))

	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	s.fetchDetails(ctx, items, studioURL, opts, out)
}

// resolveVideoPaths returns one or more listing paths that contain video_item cards.
// For series channels (Party-Hardcore, Mad-Sex-Party), it expands sub-channels first.
func (s *Scraper) resolveVideoPaths(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) []string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return []string{"/videos/all"}
	}
	host := strings.TrimPrefix(u.Hostname(), "www.")

	if host == "sinx.com" || host == "tainster.com" {
		p := strings.TrimRight(u.Path, "/")

		// /channel/{slug}/all → series channel, expand sub-channels
		if m := channelAllRe.FindStringSubmatch(p); m != nil {
			scraper.Debugf(1, "tainster: expanding series channel %s", m[1])
			return s.expandSeriesChannel(ctx, m[1], opts, out)
		}

		if girlsRe.MatchString(p) || tagRe.MatchString(p) || videosRe.MatchString(p) {
			return []string{p}
		}
		if p != "" && p != "/" {
			return []string{p}
		}
		return []string{"/videos/all"}
	}

	if info, ok := subsiteDomains[host]; ok {
		scraper.Debugf(1, "tainster: mapped %s → %s (series=%v)", host, info.slug, info.isSeries)
		if info.isSeries {
			return s.expandSeriesChannel(ctx, info.slug, opts, out)
		}
		return []string{"/" + info.slug}
	}

	return []string{"/videos/all"}
}

// expandSeriesChannel walks /channel/{slug}/all pages to collect sub-channel slugs,
// returning a path per sub-channel for video collection.
func (s *Scraper) expandSeriesChannel(ctx context.Context, seriesSlug string, opts scraper.ListOpts, out chan<- scraper.SceneResult) []string {
	var paths []string
	page := 1
	for {
		if ctx.Err() != nil {
			return paths
		}
		pageURL := fmt.Sprintf("%s/channel/%s/all?sort=newest&page=%d", s.baseURL, seriesSlug, page)
		slugs, lastPage, err := s.fetchSubChannels(ctx, pageURL)
		if err != nil {
			var se *httpx.StatusError
			if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
				scraper.Debugf(1, "tainster: sub-channels page %d: 404, stopping", page)
				break
			}
			select {
			case out <- scraper.Error(fmt.Errorf("sub-channels page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return paths
		}
		for _, slug := range slugs {
			paths = append(paths, "/"+slug)
		}
		scraper.Debugf(1, "tainster: series %s page %d: %d sub-channels", seriesSlug, page, len(slugs))

		if len(slugs) == 0 || (lastPage > 0 && page >= lastPage) {
			break
		}
		if !scraper.Pace(ctx, opts.Delay) {
			return paths
		}
		page++
	}
	scraper.Debugf(1, "tainster: series %s: %d total sub-channels", seriesSlug, len(paths))
	return paths
}

// collectItems walks a single paginated listing path and returns all video items.
func (s *Scraper) collectItems(ctx context.Context, basePath string, opts scraper.ListOpts, out chan<- scraper.SceneResult) []listingItem {
	var items []listingItem
	page := 1
	for {
		if ctx.Err() != nil {
			return items
		}
		pageURL := fmt.Sprintf("%s%s?sort=newest&page=%d", s.baseURL, basePath, page)
		pageItems, lastPage, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			var se *httpx.StatusError
			if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
				scraper.Debugf(1, "tainster: listing %s page %d: 404, stopping pagination", basePath, page)
				break
			}
			select {
			case out <- scraper.Error(fmt.Errorf("listing %s page %d: %w", basePath, page, err)):
			case <-ctx.Done():
			}
			return items
		}
		if len(pageItems) == 0 {
			break
		}
		items = append(items, pageItems...)

		if lastPage > 0 && page >= lastPage {
			break
		}
		if !scraper.Pace(ctx, opts.Delay) {
			return items
		}
		page++
	}
	return items
}

// --- listing page ---

type listingItem struct {
	id        string
	path      string
	title     string
	thumbnail string
	channel   string
}

var (
	cardRe      = regexp.MustCompile(`(?s)<figure\s+class="\s*video_item\s*"[^>]*>.*?<!-- video block end -->`)
	cardLinkRe  = regexp.MustCompile(`<a\s+href="(/[^"]+/movie/(\d+)/[^"]*)"`)
	cardTitleRe = regexp.MustCompile(`<h3\s+class="title--5"[^>]*>\s*([^<]+?)\s*</h3>`)
	cardThumbRe = regexp.MustCompile(`data-title-image="([^"]+)"`)
	cardChanRe  = regexp.MustCompile(`(?s)video_item--channel-link.*?<a[^>]+href="[^"]*"[^>]*class="link"[^>]*>\s*([^<]+?)\s*</a>`)
	lastPageRe  = regexp.MustCompile(`<li\s+class="last-page">\s*<a\s+href="[^"]*">(\d+)</a>`)
)

func (s *Scraper) fetchListing(ctx context.Context, rawURL string) ([]listingItem, int, error) {
	body, err := s.fetchPage(ctx, rawURL)
	if err != nil {
		return nil, 0, err
	}

	cards := cardRe.FindAll(body, -1)
	items := make([]listingItem, 0, len(cards))
	for _, card := range cards {
		item := listingItem{}
		if m := cardLinkRe.FindSubmatch(card); m != nil {
			item.path = string(m[1])
			item.id = string(m[2])
		} else {
			continue
		}
		if m := cardTitleRe.FindSubmatch(card); m != nil {
			item.title = html.UnescapeString(strings.TrimSpace(string(m[1])))
		}
		if m := cardThumbRe.FindSubmatch(card); m != nil {
			item.thumbnail = string(m[1])
		}
		if m := cardChanRe.FindSubmatch(card); m != nil {
			item.channel = strings.TrimSpace(string(m[1]))
		}
		items = append(items, item)
	}

	lastPage := 0
	if m := lastPageRe.FindSubmatch(body); m != nil {
		lastPage, _ = strconv.Atoi(string(m[1]))
	}

	return items, lastPage, nil
}

// --- sub-channel listing ---

var subChanLinkRe = regexp.MustCompile(`<a\s+href="/([\w][\w-]*)"[^>]*class="item--link"`)

func (s *Scraper) fetchSubChannels(ctx context.Context, rawURL string) ([]string, int, error) {
	body, err := s.fetchPage(ctx, rawURL)
	if err != nil {
		return nil, 0, err
	}

	var slugs []string
	for _, m := range subChanLinkRe.FindAllSubmatch(body, -1) {
		slugs = append(slugs, string(m[1]))
	}

	lastPage := 0
	if m := lastPageRe.FindSubmatch(body); m != nil {
		lastPage, _ = strconv.Atoi(string(m[1]))
	}

	return slugs, lastPage, nil
}

// --- HTTP helper ---

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// --- detail pages ---

var (
	detailTitleRe  = regexp.MustCompile(`<h1\s+class="title--3">([^<]+)</h1>`)
	detailDateRe   = regexp.MustCompile(`<ul\s+class="video_info-list">\s*<li>(\d{1,2}\s+\w+\s+\d{4})</li>`)
	detailDurRe    = regexp.MustCompile(`<strong>(\d+)\s+minutes</strong>`)
	detailDescRe   = regexp.MustCompile(`(?s)<h5\s+class="title--5 mb10">Description</h5>\s*<p>(.+?)</p>`)
	detailTagRe    = regexp.MustCompile(`(?s)video-page--tag.*?tags-wrap(.*?)</div>\s*</div>`)
	tagLinkRe      = regexp.MustCompile(`<span[^>]*>#?([^<]+)</span>`)
	detailPriceRe  = regexp.MustCompile(`price-format">(\d+)\.<sup>(\d+)</sup>`)
	detailPerfRe   = regexp.MustCompile(`(?s)<figure\s+class="girls-item">.*?<h4[^>]*>([^<]+)</h4>`)
	detailSeriesRe = regexp.MustCompile(`(?s)<ul\s+class="video_info-list">.*?<a[^>]+href="[^"]*"[^>]*>[^<]*<span>([^<]+)</span></a>`)
)

type sceneDetail struct {
	title       string
	date        time.Time
	duration    int
	description string
	tags        []string
	performers  []string
	price       float64
	series      string
}

func (s *Scraper) fetchDetail(ctx context.Context, path string) (sceneDetail, error) {
	body, err := s.fetchPage(ctx, s.baseURL+path)
	if err != nil {
		return sceneDetail{}, err
	}
	return parseDetail(body), nil
}

func parseDetail(body []byte) sceneDetail {
	d := sceneDetail{}

	if m := detailTitleRe.FindSubmatch(body); m != nil {
		d.title = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := detailDateRe.FindSubmatch(body); m != nil {
		t, err := time.Parse("2 Jan 2006", string(m[1]))
		if err != nil {
			t, _ = time.Parse("2 January 2006", string(m[1]))
		}
		d.date = t.UTC()
	}

	if m := detailDurRe.FindSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(string(m[1]))
		d.duration = mins * 60
	}

	if m := detailDescRe.FindSubmatch(body); m != nil {
		d.description = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := detailTagRe.FindSubmatch(body); m != nil {
		for _, tm := range tagLinkRe.FindAllSubmatch(m[1], -1) {
			tag := strings.TrimSpace(html.UnescapeString(string(tm[1])))
			if tag != "" {
				d.tags = append(d.tags, tag)
			}
		}
	}

	for _, m := range detailPerfRe.FindAllSubmatch(body, -1) {
		name := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if name != "" {
			d.performers = append(d.performers, name)
		}
	}

	matches := detailPriceRe.FindAllSubmatch(body, -1)
	if len(matches) > 0 {
		last := matches[len(matches)-1]
		whole, _ := strconv.Atoi(string(last[1]))
		cents, _ := strconv.Atoi(string(last[2]))
		d.price = float64(whole) + float64(cents)/100.0
	}

	if m := detailSeriesRe.FindSubmatch(body); m != nil {
		d.series = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}

	return d
}

// --- worker pool for detail fetching ---

func (s *Scraper) fetchDetails(ctx context.Context, items []listingItem, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "tainster: fetching %d details with %d workers", len(items), workers)

	work := make(chan listingItem, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				// A dead detail page costs the date, runtime, description,
				// tags and cast — but the listing card already names the
				// scene, its channel and its thumbnail, so dropping it traded
				// missing metadata for a missing scene that an authoritative
				// --full Save would then delete. `buildScene` already falls
				// back to the card's own title.
				detail, ferr := s.fetchDetail(ctx, item.path)
				if ferr != nil {
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.id, ferr)):
					case <-ctx.Done():
						return
					}
				}
				scene := buildScene(item, detail, studioURL, now)
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, item := range items {
		if opts.KnownIDs[item.id] {
			scraper.Debugf(1, "tainster: hit known ID %s, stopping early", item.id)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			break
		}
		select {
		case work <- item:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
}

func buildScene(item listingItem, d sceneDetail, studioURL string, now time.Time) models.Scene {
	title := d.title
	if title == "" {
		title = item.title
	}
	scene := models.Scene{
		ID:          item.id,
		SiteID:      "tainster",
		StudioURL:   studioURL,
		Title:       title,
		URL:         defaultBase + item.path,
		Thumbnail:   item.thumbnail,
		Duration:    d.duration,
		Date:        d.date,
		Description: d.description,
		Tags:        d.tags,
		Performers:  d.performers,
		Series:      d.series,
		ScrapedAt:   now,
	}
	if item.channel != "" {
		scene.Studio = item.channel
		if d.series == "" {
			scene.Series = item.channel
		}
	}
	if d.price > 0 {
		scene.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: d.price,
		})
	}
	return scene
}
