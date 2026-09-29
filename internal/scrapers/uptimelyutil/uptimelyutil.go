package uptimelyutil

import (
	"bytes"
	"context"
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

type SiteConfig struct {
	ID       string
	Studio   string
	Domain   string
	Patterns []string
	MatchRe  *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		Client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string               { return s.cfg.ID }
func (s *Scraper) Patterns() []string       { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool { return s.cfg.MatchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listingItem)
	var wg sync.WaitGroup
	scraper.Debugf(1, "%s: fetching detail pages with %d workers", s.cfg.ID, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				detailURL := buildDetailURL(studioURL, item.code, s.cfg.Domain)
				scene, err := s.fetchDetail(ctx, studioURL, item, detailURL, opts.Delay)
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)
		if isCatalogueURL(studioURL) {
			s.discoverCatalogue(ctx, studioURL, opts, out, work)
			return
		}
		s.discoverListing(ctx, normalizeListURL(studioURL), opts, out, work)
	}()

	wg.Wait()
}

// discoverListing walks one `/works/list/...` or `/actress/detail/...` view.
// These are date-sorted, so the KnownIDs early-stop applies.
func (s *Scraper) discoverListing(ctx context.Context, baseURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingItem) {
	seen := map[string]bool{}
	for page := 1; ; page++ {
		if page > 1 && !sleep(ctx, opts.Delay) {
			return
		}

		scraper.Debugf(1, "%s: fetching page %d", s.cfg.ID, page)
		pageURL := buildPageURL(baseURL, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		items := parseListingItems(body)
		if len(items) == 0 {
			if page == 1 {
				// A listing whose first page holds nothing is a template
				// change or a URL that does not address a listing at all —
				// both indistinguishable from "this view is empty" unless it
				// is said out loud. Reporting it also keeps the traversal
				// marked incomplete, so an authoritative save cannot read the
				// silence as a catalogue that vanished.
				select {
				case out <- scraper.Error(scraper.ParseError(pageURL, fmt.Errorf("no works on the first listing page"))):
				case <-ctx.Done():
				}
			}
			return
		}

		if page == 1 {
			total := extractTotal(body)
			if total <= 0 {
				total = len(items)
			}
			scraper.Debugf(1, "%s: %d total scenes", s.cfg.ID, total)
			select {
			case out <- scraper.Progress(total):
			case <-ctx.Done():
				return
			}
		}

		newItems := 0
		for _, item := range items {
			if opts.KnownIDs[item.code] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.ID, item.code)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			if seen[item.code] {
				continue
			}
			seen[item.code] = true
			newItems++
			select {
			case work <- item:
			case <-ctx.Done():
				return
			}
		}
		if newItems == 0 {
			return
		}
	}
}

// discoverCatalogue enumerates the whole label from the genre index.
//
// `/works/list/release` is the obvious catalogue entry point and is the wrong
// one: it is a single unpaginated page of the newest ~20 works, and `?page=N`
// re-serves it unchanged, so a walk of it reaches 19 of Honnaka's ~350 works
// and stops. The genre listings are the only view that both paginates and
// carries a `全N作品中` count, and their union is the catalogue — on
// tameikegoro.jp it reaches 2406 works against 22 from the release page, and
// the 163 series listings add nothing the genres miss beyond 25 works.
//
// Works stream to the detail pool as they are discovered rather than after the
// whole index is walked: a full genre sweep is a few hundred requests, and
// holding every scene back until it finishes would leave a scrape looking hung
// for minutes. The consequence is that the true count is only known at the end,
// so the progress total is sent then — the cmd layer shows a running count
// until it arrives.
func (s *Scraper) discoverCatalogue(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingItem) {
	origin := originOf(studioURL, s.cfg.Domain)

	body, err := s.fetchPage(ctx, origin+"/works/genre")
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("genre index: %w", err)):
		case <-ctx.Done():
		}
		return
	}
	genres := parseGenreIDs(body)
	if len(genres) == 0 {
		select {
		case out <- scraper.Error(scraper.ParseError(origin+"/works/genre",
			fmt.Errorf("no genre listings found"))):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: %d genre listings to walk", s.cfg.ID, len(genres))

	seen := map[string]bool{}
	sent, skipped := 0, 0
	for _, id := range genres {
		if ctx.Err() != nil {
			return
		}
		listURL := fmt.Sprintf("%s/works/list/genre/%s", origin, id)
		for page := 1; ; page++ {
			if !sleep(ctx, opts.Delay) {
				return
			}
			body, err := s.fetchPage(ctx, buildPageURL(listURL, page))
			if err != nil {
				// One unreachable genre is not the catalogue. Report it and
				// keep walking: bailing here would hand an authoritative
				// --full save a catalogue missing everything after it.
				select {
				case out <- scraper.Error(fmt.Errorf("genre %s page %d: %w", id, page, err)):
				case <-ctx.Done():
					return
				}
				break
			}
			items := parseListingItems(body)
			if len(items) == 0 {
				break
			}
			fresh := 0
			for _, item := range items {
				if seen[item.code] {
					continue
				}
				seen[item.code] = true
				fresh++
				// KnownIDs never truncates this walk — the union is in genre
				// order, not date order, so stopping at the first known code
				// would drop everything after it. Stored works are skipped
				// instead, which costs the detail fetch and nothing else.
				if opts.KnownIDs[item.code] {
					skipped++
					continue
				}
				select {
				case work <- item:
					sent++
				case <-ctx.Done():
					return
				}
			}
			// A genre whose page repeats wholesale is at its end; the CMS
			// clamps rather than 404ing past the last page.
			if fresh == 0 {
				break
			}
		}
	}
	scraper.Debugf(1, "%s: %d works across %d genres (%d already stored)", s.cfg.ID, len(seen), len(genres), skipped)

	// Reported last because it is only knowable last, and reported as the
	// number actually being fetched so the progress line does not stall short
	// of its own total.
	select {
	case out <- scraper.Progress(sent):
	case <-ctx.Done():
		return
	}
	if skipped > 0 {
		// Says the traversal was deliberately partial, so an authoritative
		// save cannot read the smaller result as "the rest is gone".
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// ---- URL helpers ----

var pageParamRe = regexp.MustCompile(`[?&]page=\d+`)

// listingPathRe marks the URLs that address one view of the catalogue. Anything
// else on a covered host — the apex, /top — is a request for the whole label.
var listingPathRe = regexp.MustCompile(`/(?:works/list/|works/detail/|actress/detail/)`)

var genreIDRe = regexp.MustCompile(`works/list/genre/(\d+)`)

func isCatalogueURL(u string) bool { return !listingPathRe.MatchString(u) }

func parseGenreIDs(body []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range genreIDRe.FindAllSubmatch(body, -1) {
		id := string(m[1])
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// originOf keeps the operator's own spelling of the host so an http-only or
// non-www form stays addressable, and so a test server survives the rewrite.
func originOf(studioURL, fallbackDomain string) string {
	if u, err := url.Parse(studioURL); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return "https://" + fallbackDomain
}

func normalizeListURL(u string) string {
	u = pageParamRe.ReplaceAllString(u, "")
	u = strings.TrimRight(u, "?&")
	return u
}

func buildPageURL(baseURL string, page int) string {
	if page == 1 {
		return baseURL
	}
	sep := "?"
	if strings.Contains(baseURL, "?") {
		sep = "&"
	}
	return baseURL + sep + "page=" + strconv.Itoa(page)
}

func buildDetailURL(studioURL, code, fallbackDomain string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "https://" + fallbackDomain + "/works/detail/" + code
	}
	u.Path = "/works/detail/" + code
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// ---- listing page parsing ----

type listingItem struct {
	code  string
	thumb string
}

var (
	detailCodeRe = regexp.MustCompile(`href="[^"]*works/detail/([A-Z0-9]+)`)
	dataSrcRe    = regexp.MustCompile(`data-src="([^"]+)"`)
)

func parseListingItems(body []byte) []listingItem {
	matches := detailCodeRe.FindAllSubmatchIndex(body, -1)
	seen := map[string]bool{}
	var items []listingItem
	for _, loc := range matches {
		code := string(body[loc[2]:loc[3]])
		if seen[code] {
			continue
		}
		seen[code] = true
		end := loc[1] + 500
		if end > len(body) {
			end = len(body)
		}
		thumb := ""
		if m := dataSrcRe.FindSubmatch(body[loc[1]:end]); m != nil {
			thumb = string(m[1])
			if strings.Contains(thumb, "logo") || strings.Contains(thumb, "X_logo") {
				thumb = ""
			}
		}
		items = append(items, listingItem{code: code, thumb: thumb})
	}
	return items
}

var totalRe = regexp.MustCompile(`全(\d+)作品中`)

func extractTotal(body []byte) int {
	m := totalRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// ---- detail page parsing ----

var (
	detailTitleRe = regexp.MustCompile(`class="p-workPage__title">\s*([^<]+)`)
	detailDescRe  = regexp.MustCompile(`class="p-workPage__text">([^<]+)</p>`)
	detailDateRe  = regexp.MustCompile(`list/date/(\d{4})-(\d{2})-(\d{2})`)
	durValRe      = regexp.MustCompile(`(\d+)分`)
	dirValRe      = regexp.MustCompile(`<p>([^<]+)</p>`)
	actressLinkRe = regexp.MustCompile(`href="[^"]*actress/detail/[^"]*"[^>]*>([^<]+)</a>`)
	genreLinkRe   = regexp.MustCompile(`href="[^"]*genre/[^"]*"[^>]*>([^<]+)</a>`)
	seriesLinkRe  = regexp.MustCompile(`href="[^"]*series/[^"]*"[^>]*>([^<]+)</a>`)
)

func (s *Scraper) fetchDetail(ctx context.Context, studioURL string, item listingItem, detailURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	body, err := s.fetchPage(ctx, detailURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", item.code, err)
	}

	return parseDetail(body, s.cfg.ID, s.cfg.Studio, studioURL, item, detailURL), nil
}

func parseDetail(body []byte, siteID, studio, studioURL string, item listingItem, detailURL string) models.Scene {
	scene := models.Scene{
		ID:        item.code,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       detailURL,
		Studio:    studio,
		Thumbnail: item.thumb,
		ScrapedAt: time.Now().UTC(),
	}

	if m := detailTitleRe.FindSubmatch(body); m != nil {
		scene.Title = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := detailDescRe.FindSubmatch(body); m != nil {
		scene.Description = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	table := extractTable(body)

	scene.Performers = extractLinkTexts(actressLinkRe, table)
	scene.Tags = extractLinkTexts(genreLinkRe, table)
	if series := extractLinkTexts(seriesLinkRe, table); len(series) > 0 {
		scene.Series = series[0]
	}

	if m := detailDateRe.FindSubmatch(table); m != nil {
		y, _ := strconv.Atoi(string(m[1]))
		mo, _ := strconv.Atoi(string(m[2]))
		d, _ := strconv.Atoi(string(m[3]))
		scene.Date = time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	}

	if after := bytesAfter(table, "収録時間</div>"); after != nil {
		if m := durValRe.FindSubmatch(after); m != nil {
			mins, _ := strconv.Atoi(string(m[1]))
			scene.Duration = mins * 60
		}
	}

	if after := bytesAfter(table, "監督</div>"); after != nil {
		if m := dirValRe.FindSubmatch(after); m != nil {
			scene.Director = strings.TrimSpace(html.UnescapeString(string(m[1])))
		}
	}

	return scene
}

func extractTable(body []byte) []byte {
	marker := []byte(`p-workPage__table">`)
	start := bytes.Index(body, marker)
	if start < 0 {
		return body
	}
	start += len(marker)
	end := bytes.Index(body[start:], []byte(`p-workPage__side`))
	if end < 0 {
		return body[start:]
	}
	return body[start : start+end]
}

func bytesAfter(body []byte, marker string) []byte {
	idx := bytes.Index(body, []byte(marker))
	if idx < 0 {
		return nil
	}
	return body[idx+len(marker):]
}

func extractLinkTexts(re *regexp.Regexp, body []byte) []string {
	matches := re.FindAllSubmatch(body, -1)
	var names []string
	for _, m := range matches {
		name := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// ---- HTTP ----

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
