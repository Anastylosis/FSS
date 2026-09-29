package woodman

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

const (
	siteID   = "woodman"
	siteBase = "https://www.woodmancastingx.com"
	pageSize = 20
	// maxPages bounds the listing walk at 20 scenes per page — far beyond the
	// catalogue, and a backstop against a CMS that never reports an end.
	maxPages = 500
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?woodmancastingx\.com(?:/|$)`)
	girlRe  = regexp.MustCompile(`/girl/([\w-]+_\d+)`)
)

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		Client: httpx.NewClient(30 * time.Second),
		base:   siteBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"woodmancastingx.com/",
		"woodmancastingx.com/girl/{slug}_{id}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if m := girlRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping performer page %s", siteID, m[1])
		s.runGirl(ctx, studioURL, opts, out)
		return
	}

	scraper.Debugf(1, "%s: scraping main listing", siteID)
	s.runListing(ctx, studioURL, opts, out)
}

// runListing paginates through /new?page=N and spawns a worker pool for detail pages.
func (s *Scraper) runListing(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listingScene)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ls := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, ls, studioURL)
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

	go func() {
		defer close(work)
		s.enqueuePages(ctx, studioURL, opts, out, work)
	}()

	wg.Wait()
}

func (s *Scraper) enqueuePages(ctx context.Context, _ string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	// The walk ends on a short or empty page, but a CMS that clamps an
	// out-of-range `page` back to page 1 returns a full page forever. A page
	// yielding no id the walk has not already emitted is that echo; maxPages
	// is the backstop.
	seen := map[string]bool{}
	for page := 1; page <= maxPages; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "%s: fetching page %d", siteID, page)

		u := fmt.Sprintf("%s/new?page=%d", s.base, page)
		body, err := s.fetchPage(ctx, u)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		scenes := parseListingPage(body, s.base)
		if len(scenes) == 0 {
			return
		}

		if page == 1 {
			maxPage := parseMaxPage(body)
			if maxPage > 0 {
				total := maxPage * len(scenes)
				scraper.Debugf(1, "%s: ~%d total scenes (%d pages)", siteID, total, maxPage)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
		}

		fresh := 0
		for _, ls := range scenes {
			if seen[ls.id] {
				continue
			}
			seen[ls.id] = true
			fresh++
			if opts.KnownIDs != nil && opts.KnownIDs[ls.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, ls.id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- ls:
			case <-ctx.Done():
				return
			}
		}

		if fresh == 0 {
			scraper.Debugf(1, "%s: page %d repeated scenes already seen, stopping", siteID, page)
			return
		}
		if len(scenes) < pageSize {
			return
		}
	}
	scraper.Debugf(1, "%s: stopped at the %d-page cap", siteID, maxPages)
}

// runGirl fetches a performer page and emits scenes directly (no detail fetch needed
// since performer pages are single-page with no pagination).
func (s *Scraper) runGirl(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetchPage(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	now := time.Now().UTC()
	girlName, scenes := parseGirlPage(body, s.base)
	scraper.Debugf(1, "%s: performer %s has %d scenes", siteID, girlName, len(scenes))

	if len(scenes) > 0 {
		select {
		case out <- scraper.Progress(len(scenes)):
		case <-ctx.Done():
			return
		}
	}

	// The performer page is a single fetch that is already fully parsed, so
	// stopping at a known scene saves no request and simply drops every scene
	// after it. Skip the stored ones and keep going.
	skipped := 0
	for _, ls := range scenes {
		if opts.KnownIDs != nil && opts.KnownIDs[ls.id] {
			skipped++
			continue
		}

		scene := models.Scene{
			ID:         ls.id,
			SiteID:     siteID,
			StudioURL:  studioURL,
			Title:      ls.title,
			URL:        ls.url,
			Studio:     "Woodman Casting X",
			Thumbnail:  ls.thumb,
			Duration:   ls.duration,
			Performers: []string{girlName},
			ScrapedAt:  now,
		}
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}

	if skipped > 0 {
		scraper.Debugf(1, "%s: skipped %d already-stored scene(s)", siteID, skipped)
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: defaultHeaders(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, studioURL string) (models.Scene, error) {
	body, err := s.fetchPage(ctx, ls.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", ls.id, err)
	}

	det := parseDetailPage(body, s.base)

	scene := models.Scene{
		ID:        ls.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     ls.title,
		URL:       ls.url,
		Studio:    "Woodman Casting X",
		Thumbnail: ls.thumb,
		ScrapedAt: time.Now().UTC(),
	}

	if det.title != "" {
		scene.Title = det.title
	}
	scene.Description = det.description
	scene.Tags = det.tags
	scene.Performers = det.performers

	if !det.date.IsZero() {
		scene.Date = det.date
	} else if !ls.date.IsZero() {
		scene.Date = ls.date
	}

	if det.duration > 0 {
		scene.Duration = det.duration
	} else if ls.duration > 0 {
		scene.Duration = ls.duration
	}

	return scene, nil
}

func defaultHeaders() map[string]string {
	return httpx.BrowserHeaders(httpx.UserAgentFirefox)
}

// ---- listing page parsing ----

type listingScene struct {
	id       string
	url      string
	title    string
	thumb    string
	date     time.Time
	duration int
}

var (
	dayTitleRe   = regexp.MustCompile(`(?s)<div class="(?:even|odd) day_title">\s*(.*?)\s*</div>`)
	elementRe    = regexp.MustCompile(`(?s)<div class="element">(.*?)<div class="clear">`)
	sceneHrefRe  = regexp.MustCompile(`<a class="item scene\s*" href="([^"]+)" title="([^"]*)"`)
	thumbRe      = regexp.MustCompile(`<img class="thumb" src="([^"]+)"`)
	detailsRe    = regexp.MustCompile(`<p class="details">([^<]+)</p>`)
	nameRe       = regexp.MustCompile(`(?s)<p class="name"><a[^>]*>(.*?)</a></p>`)
	paginationRe = regexp.MustCompile(`(?s)<div class="pagination">(.*?)</div>`)
	lastPageRe   = regexp.MustCompile(`<a href="/new\?page=(\d+)">Last</a>`)
	pageNumRe    = regexp.MustCompile(`>(\d+)</a>`)
)

func parseListingPage(body []byte, base string) []listingScene {
	page := string(body)

	type datePos struct {
		pos  int
		date time.Time
	}
	var dates []datePos
	for _, loc := range dayTitleRe.FindAllStringSubmatchIndex(page, -1) {
		text := strings.TrimSpace(page[loc[2]:loc[3]])
		dates = append(dates, datePos{pos: loc[0], date: parseListingDate(text)})
	}

	var scenes []listingScene
	for _, m := range elementRe.FindAllStringSubmatchIndex(page, -1) {
		elem := page[m[2]:m[3]]
		ls := parseElement(elem, base)
		if ls.id == "" {
			continue
		}
		elemPos := m[0]
		for i := len(dates) - 1; i >= 0; i-- {
			if dates[i].pos < elemPos {
				ls.date = dates[i].date
				break
			}
		}
		scenes = append(scenes, ls)
	}
	return scenes
}

func parseElement(elem, base string) listingScene {
	var ls listingScene

	m := sceneHrefRe.FindStringSubmatch(elem)
	if m == nil {
		return ls
	}
	href := m[1]
	// Skip external WUNF links
	if strings.Contains(href, "wakeupnfuck.com") {
		return ls
	}
	if !strings.HasPrefix(href, "http") {
		href = base + href
	}
	ls.url = href
	ls.title = html.UnescapeString(m[2])
	ls.id = extractID(href)

	if m := thumbRe.FindStringSubmatch(elem); m != nil {
		ls.thumb = m[1]
	}

	if m := detailsRe.FindStringSubmatch(elem); m != nil {
		ls.duration = parseDurationText(strings.TrimSpace(m[1]))
	}

	if m := nameRe.FindStringSubmatch(elem); m != nil {
		title := strings.TrimSpace(html.UnescapeString(m[1]))
		title = strings.ReplaceAll(title, " ", " ")
		title = strings.TrimSuffix(title, "* UPDATED *")
		title = strings.TrimSpace(title)
		if title != "" {
			ls.title = title
		}
	}

	return ls
}

var sceneIDRe = regexp.MustCompile(`_(\d+)(?:\.html)?$`)

func extractID(u string) string {
	if m := sceneIDRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

func parseListingDate(s string) time.Time {
	s = parseutil.StripOrdinalSuffix(s)
	t, err := time.Parse("January 2, 2006", s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// parseDurationText handles "1 H 15 mn", "31 mn", "20 mn", etc.
func parseDurationText(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))

	// Try colon format first: "31:35" or "1:19:38"
	if d := parseutil.ParseDurationColon(s); d > 0 {
		return d
	}

	var total int
	hRe := regexp.MustCompile(`(\d+)\s*h`)
	mRe := regexp.MustCompile(`(\d+)\s*mn`)

	if m := hRe.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		total += h * 3600
	}
	if m := mRe.FindStringSubmatch(s); m != nil {
		mn, _ := strconv.Atoi(m[1])
		total += mn * 60
	}
	return total
}

func parseMaxPage(body []byte) int {
	page := string(body)

	if m := lastPageRe.FindStringSubmatch(page); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}

	m := paginationRe.FindStringSubmatch(page)
	if m == nil {
		return 1
	}
	maxPage := 1
	for _, pm := range pageNumRe.FindAllStringSubmatch(m[1], -1) {
		n, _ := strconv.Atoi(pm[1])
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

// ---- detail page parsing ----

type detailData struct {
	title       string
	description string
	date        time.Time
	duration    int
	tags        []string
	performers  []string
}

var (
	detTitleRe = regexp.MustCompile(`(?s)<h1 class="full_length">([^<]+)</h1>`)
	detDescRe  = regexp.MustCompile(`(?s)<p class="description">(.*?)</p>`)
	detDateRe  = regexp.MustCompile(`<span class="label_info">Published</span>\s*:\s*(\d{4}-\d{2}-\d{2})`)
	detDurRe   = regexp.MustCompile(`(?s)<span class="label_info">Length</span>\s*:.*?<span class="yellow">([^<]+)</span>`)
	detTagRe   = regexp.MustCompile(`<a href="/keywords/[^"]*" class="tag">([^<]+)</a>`)
	detGirlRe  = regexp.MustCompile(`<a class="girl_item"[^>]*>\s*<span class="name">([^<]+)</span>`)
	htmlTagRe  = regexp.MustCompile(`<[^>]+>`)
)

func parseDetailPage(body []byte, _ string) detailData {
	var d detailData
	page := string(body)

	if m := detTitleRe.FindStringSubmatch(page); m != nil {
		d.title = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	if m := detDescRe.FindStringSubmatch(page); m != nil {
		raw := htmlTagRe.ReplaceAllString(m[1], " ")
		d.description = strings.Join(strings.Fields(html.UnescapeString(raw)), " ")
	}

	if m := detDateRe.FindStringSubmatch(page); m != nil {
		if t, err := time.Parse("2006-01-02", m[1]); err == nil {
			d.date = t.UTC()
		}
	}

	if m := detDurRe.FindStringSubmatch(page); m != nil {
		d.duration = parseDetailDuration(strings.TrimSpace(m[1]))
	}

	for _, m := range detTagRe.FindAllStringSubmatch(page, -1) {
		tag := strings.TrimSpace(html.UnescapeString(m[1]))
		if tag != "" && tag != "More Tags ..." {
			d.tags = append(d.tags, tag)
		}
	}

	for _, m := range detGirlRe.FindAllStringSubmatch(page, -1) {
		name := strings.TrimSpace(html.UnescapeString(m[1]))
		if name != "" {
			d.performers = append(d.performers, titleCase(name))
		}
	}

	return d
}

// parseDetailDuration handles "1 hour 20 minutes", "41 minutes", etc.
func parseDetailDuration(s string) int {
	s = strings.ToLower(s)
	var total int

	hRe := regexp.MustCompile(`(\d+)\s*hour`)
	mRe := regexp.MustCompile(`(\d+)\s*min`)

	if m := hRe.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		total += h * 3600
	}
	if m := mRe.FindStringSubmatch(s); m != nil {
		mn, _ := strconv.Atoi(m[1])
		total += mn * 60
	}
	return total
}

// titleCase converts "SCARLETT SPARK" to "Scarlett Spark".
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}

// ---- performer page parsing ----

var (
	girlNameRe  = regexp.MustCompile(`(?s)<div class="infos">\s*<h1>([^<]+)</h1>`)
	girlSceneRe = regexp.MustCompile(`(?s)<a class="item scene"[^>]*href="([^"]+)"[^>]*>\s*<img class="thumb[^"]*" src="([^"]*)"[^/]*/>\s*<span class="title">([^<]+)</span>\s*<span class="infos">([^<]*)</span>`)
)

func parseGirlPage(body []byte, base string) (string, []listingScene) {
	page := string(body)

	var girlName string
	if m := girlNameRe.FindStringSubmatch(page); m != nil {
		girlName = titleCase(strings.TrimSpace(html.UnescapeString(m[1])))
	}

	var scenes []listingScene
	for _, m := range girlSceneRe.FindAllStringSubmatch(page, -1) {
		href := m[1]
		if strings.Contains(href, "wakeupnfuck.com") {
			continue
		}
		if !strings.HasPrefix(href, "http") {
			href = base + href
		}

		id := extractID(href)
		if id == "" {
			continue
		}

		ls := listingScene{
			id:    id,
			url:   href,
			thumb: m[2],
			title: strings.TrimSpace(html.UnescapeString(m[3])),
		}

		infoStr := strings.TrimSpace(m[4])
		ls.duration = parseDetailDuration(infoStr)

		scenes = append(scenes, ls)
	}
	return girlName, scenes
}
