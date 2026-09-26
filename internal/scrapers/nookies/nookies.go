package nookies

import (
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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func init() { scraper.Register(New()) }

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://nookies.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return "nookies" }

func (s *Scraper) Patterns() []string {
	return []string{
		"nookies.com/videos",
		"nookies.com/site/{slug}",
		"nookies.com/model/{slug}",
		"nookies.com/tag/{slug}",
		"milfaf.com, gilfaf.com, breedme.com, shadyspa.com, over40handjobs.com (new CMS)",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?nookies\.com(?:/|$)`)

// newCMSRe matches the five Nookies brands that run the richer Laravel/Vite
// CMS on their own standalone domain (schema.org VideoObject detail pages).
// The captured group is the brand slug, which is also the SiteID.
var newCMSRe = regexp.MustCompile(`(?i)^https?://(?:www\.)?(milfaf|gilfaf|breedme|shadyspa|over40handjobs)\.com(?:/|$)`)

// studioNames maps a new-CMS brand slug to its display name.
var studioNames = map[string]string{
	"milfaf":         "MilfAF",
	"gilfaf":         "GilfAF",
	"breedme":        "BreedMe",
	"shadyspa":       "ShadySpa",
	"over40handjobs": "Over 40 Handjobs",
}

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u) || newCMSRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardRe      = regexp.MustCompile(`(?s)<div class="video-card">.*?<!-- End video-card-->`)
	cardURLRe   = regexp.MustCompile(`<a href="/video/(\d+)/([^"]+)"`)
	cardThumbRe = regexp.MustCompile(`<img src="([^"]+)" alt="([^"]*)"[^>]*class="main-video-img`)
	cardPerfRe  = regexp.MustCompile(`<a href="/model/[^"]*" class="tag-btn">([^<]+)</a>`)
	cardDateRe  = regexp.MustCompile(`<span class="date">(\d{4}-\d{2}-\d{2})</span>`)
	cardSiteRe  = regexp.MustCompile(`<a href="/site/([^"]+)">`)

	nextPageRe = regexp.MustCompile(`<a[^>]+href="[^"]*\?page=(\d+)"[^>]*>\s*»`)

	detailTitleRe = regexp.MustCompile(`<h1[^>]*>([^<]+)</h1>`)
	detailDescRe  = regexp.MustCompile(`(?s)<h3>Description:</h3>\s*<p>(.*?)</p>`)
	detailDurRe   = regexp.MustCompile(`(?s)<i class="fa-solid fa-video"></i>\s*(\d+:\d{2})`)
	detailTagRe   = regexp.MustCompile(`<a class="pill-link" href="/tag/[^"]*">([^<]+)</a>`)

	modelRe = regexp.MustCompile(`/model/`)
	siteRe  = regexp.MustCompile(`/site/`)
	tagRe   = regexp.MustCompile(`/tag/`)
)

type listItem struct {
	id         string
	url        string
	title      string
	thumbnail  string
	performers []string
	date       time.Time
	subSite    string
}

type detailData struct {
	title       string
	description string
	duration    int
	tags        []string
}

func parseListingPage(body []byte) []listItem {
	cards := cardRe.FindAll(body, -1)
	items := make([]listItem, 0, len(cards))
	for _, card := range cards {
		if it, ok := parseCard(card); ok {
			items = append(items, it)
		}
	}
	return items
}

func parseCard(card []byte) (listItem, bool) {
	m := cardURLRe.FindSubmatch(card)
	if m == nil {
		return listItem{}, false
	}

	it := listItem{
		id:  string(m[1]),
		url: "/video/" + string(m[1]) + "/" + string(m[2]),
	}

	if mThumb := cardThumbRe.FindSubmatch(card); mThumb != nil {
		it.thumbnail = string(mThumb[1])
		it.title = html.UnescapeString(strings.TrimSpace(string(mThumb[2])))
	}

	for _, mPerf := range cardPerfRe.FindAllSubmatch(card, -1) {
		name := strings.TrimSpace(html.UnescapeString(string(mPerf[1])))
		if name != "" {
			it.performers = append(it.performers, name)
		}
	}

	if mDate := cardDateRe.FindSubmatch(card); mDate != nil {
		if t, err := time.Parse("2006-01-02", string(mDate[1])); err == nil {
			it.date = t
		}
	}

	if mSite := cardSiteRe.FindSubmatch(card); mSite != nil {
		it.subSite = string(mSite[1])
	}

	return it, true
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	// New-CMS brands (milfaf.com et al.) on their own domain use the
	// VideoObject path. nookies.com URLs — including nookies.com/site/milfaf —
	// fall through to the old-CMS hub logic below.
	if m := newCMSRe.FindStringSubmatch(studioURL); m != nil {
		slug := strings.ToLower(m[1])
		scraper.Debugf(1, "nookies: detected new CMS brand %q", slug)
		s.runNewCMS(ctx, studioURL, slug, opts, out)
		return
	}

	switch {
	case modelRe.MatchString(studioURL):
		scraper.Debugf(1, "nookies: detected model page")
		s.scrapeSinglePage(ctx, studioURL, opts, out)
	case siteRe.MatchString(studioURL):
		scraper.Debugf(1, "nookies: detected site page")
		s.scrapePaginated(ctx, studioURL, opts, out)
	case tagRe.MatchString(studioURL):
		scraper.Debugf(1, "nookies: detected tag page")
		s.scrapePaginated(ctx, studioURL, opts, out)
	default:
		s.scrapePaginated(ctx, s.base+"/videos", opts, out)
	}
}

func (s *Scraper) scrapeSinglePage(ctx context.Context, pageURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	items := parseListingPage(body)
	if len(items) == 0 {
		return
	}

	scraper.Debugf(1, "nookies: found %d scenes on single page", len(items))
	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	now := time.Now().UTC()
	scenes := s.fetchDetails(ctx, items, opts, now)
	for _, sc := range scenes {
		if opts.KnownIDs[sc.ID] {
			scraper.Debugf(1, "nookies: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(sc):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) scrapePaginated(ctx context.Context, baseURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, "nookies", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := baseURL
		if page > 1 {
			if strings.Contains(baseURL, "?") {
				pageURL = fmt.Sprintf("%s&page=%d", baseURL, page)
			} else {
				pageURL = fmt.Sprintf("%s?page=%d", baseURL, page)
			}
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListingPage(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := estimateTotal(body, len(items))
		scenes := s.fetchDetails(ctx, items, opts, now)
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			// If every detail fetch failed, scenes is empty while the page had
			// items; keep going until hasNextPage reports the end.
			Continue: len(items) > 0,
			Done:     !hasNextPage(body),
		}, nil
	})
}

func estimateTotal(body []byte, perPage int) int {
	if m := nextPageRe.FindSubmatch(body); m == nil {
		return perPage
	}
	maxPage := 1
	for _, m := range regexp.MustCompile(`\?page=(\d+)`).FindAllSubmatch(body, -1) {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

func hasNextPage(body []byte) bool {
	return nextPageRe.Match(body)
}

func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	scraper.Debugf(1, "nookies: fetching %d details with %d workers", len(items), workers)

	type enriched struct {
		item   listItem
		detail detailData
		err    error
	}

	results := make([]enriched, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(idx int, item listItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}

			detail, err := s.fetchDetail(ctx, s.base+item.url)
			results[idx] = enriched{item: item, detail: detail, err: err}
		}(i, it)
	}
	wg.Wait()

	var scenes []models.Scene
	for _, r := range results {
		if ctx.Err() != nil {
			break
		}
		if r.err != nil || r.item.id == "" {
			continue
		}
		scenes = append(scenes, toScene(s.base, r.item, r.detail, now))
	}
	return scenes
}

func (s *Scraper) fetchDetail(ctx context.Context, rawURL string) (detailData, error) {
	body, err := s.fetchPage(ctx, rawURL)
	if err != nil {
		return detailData{}, err
	}
	return parseDetailPage(body), nil
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := detailTitleRe.FindSubmatch(body); m != nil {
		d.title = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := detailDescRe.FindSubmatch(body); m != nil {
		d.description = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := detailDurRe.FindSubmatch(body); m != nil {
		d.duration = parseutil.ParseDurationColon(string(m[1]))
	}

	seen := make(map[string]bool)
	for _, m := range detailTagRe.FindAllSubmatch(body, -1) {
		tag := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if tag != "" && !seen[tag] {
			seen[tag] = true
			d.tags = append(d.tags, tag)
		}
	}

	return d
}

func toScene(base string, it listItem, d detailData, now time.Time) models.Scene {
	siteID := "nookies"
	if it.subSite != "" {
		siteID = it.subSite
	}

	title := it.title
	if d.title != "" {
		title = d.title
	}

	return models.Scene{
		ID:          it.id,
		SiteID:      siteID,
		StudioURL:   base,
		Title:       title,
		URL:         base + it.url,
		Thumbnail:   it.thumbnail,
		Date:        it.date,
		Duration:    d.duration,
		Performers:  it.performers,
		Description: d.description,
		Tags:        d.tags,
		ScrapedAt:   now,
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

// ---- new CMS (own-domain VideoObject) ----

var (
	// newVideoHrefRe matches the slugless `/video/{id}` links the new CMS uses
	// on listing pages (the old CMS uses `/video/{id}/{slug}`).
	newVideoHrefRe = regexp.MustCompile(`/video/(\d+)(?:[/"?#]|\\)`)
	pageNumRe      = regexp.MustCompile(`[?&]page=(\d+)`)
	// genreBlockRe / quotedRe pull the VideoObject "genre" array (scene tags).
	// parseutil.VideoObject only carries "keywords", so genre is parsed here.
	genreBlockRe = regexp.MustCompile(`(?s)"genre"\s*:\s*\[(.*?)\]`)
	quotedRe     = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	// tagPillRe pulls the visible tag pills from a detail page. On brands
	// where the VideoObject JSON-LD's "genre" array is absent or only
	// present on some scenes (e.g. over40handjobs), the tag pills are always
	// rendered, so they're used as a fallback when genre yields nothing.
	tagPillRe = regexp.MustCompile(`(?s)href="[^"]*/tag/[^"]*"[^>]*>\s*([^<]+?)\s*</a>`)
)

// runNewCMS walks the new-CMS listing on the brand's own domain and enriches
// each new scene from its `/video/{id}` detail page's VideoObject JSON-LD.
// The listing path is taken from the studio URL when it is a /tag/ or /models/
// filter; otherwise the full /videos catalogue is scraped.
func (s *Scraper) runNewCMS(ctx context.Context, studioURL, slug string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	base, listPath := newCMSBaseAndPath(studioURL, slug)
	now := time.Now().UTC()
	firstPage := true
	maxPage := 0

	scraper.Paginate(ctx, opts, "nookies/"+slug, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?page=%d", base, listPath, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		ids := newListVideoIDs(body)
		if len(ids) == 0 {
			return scraper.PageResult{}, nil
		}

		// The pager is windowed, so a later page can name a higher last page
		// than the first one did; keep the highest seen. A pager naming only
		// page 1 is *unknown*, not one page — a filtered listing short enough
		// to render no pager would otherwise end after its first page.
		if seen := maxPageNum(body); seen > maxPage {
			maxPage = seen
		}

		total := 0
		if firstPage {
			firstPage = false
			total = maxPage * len(ids)
		}

		scenes := s.fetchNewScenes(ctx, ids, slug, base, studioURL, opts, now)
		// If every detail fetch failed, scenes is empty while the page had ids;
		// keep going (the terminal page is the empty-ids early return above).
		return scraper.PageResult{
			Scenes:   scenes,
			Total:    total,
			Continue: len(ids) > 0,
			Done:     maxPage > 1 && page >= maxPage,
		}, nil
	})
}

// newCMSBaseAndPath returns the origin (e.g. https://www.milfaf.com) and the
// listing path to paginate. A /tag/ or /models/ path in the studio URL is
// preserved so those filtered listings can be scraped; anything else defaults
// to the full /videos catalogue.
func newCMSBaseAndPath(studioURL, slug string) (base, listPath string) {
	base = "https://www." + slug + ".com"
	listPath = "/videos"
	if u, err := url.Parse(studioURL); err == nil {
		if u.Scheme != "" && u.Host != "" {
			base = u.Scheme + "://" + u.Host
		}
		p := strings.TrimRight(u.Path, "/")
		if strings.HasPrefix(p, "/tag/") || strings.HasPrefix(p, "/models/") || strings.HasPrefix(p, "/model/") {
			listPath = p
		}
	}
	return base, listPath
}

// newListVideoIDs returns the `/video/{id}` IDs in document order, deduped.
func newListVideoIDs(body []byte) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, m := range newVideoHrefRe.FindAllSubmatch(body, -1) {
		id := string(m[1])
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func maxPageNum(body []byte) int {
	maxPage := 1
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

// fetchNewScenes enriches each listing ID with its VideoObject detail page,
// using a worker pool. Order is preserved so Paginate's KnownIDs early-stop
// works: known IDs become lightweight stubs (no detail fetch) that trigger the
// stop, and detail-fetch failures are dropped.
func (s *Scraper) fetchNewScenes(ctx context.Context, ids []string, slug, base, studioURL string, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "nookies: fetching %d %s details with %d workers", len(ids), slug, workers)

	results := make([]models.Scene, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, id := range ids {
		if ctx.Err() != nil {
			break
		}
		if opts.KnownIDs[id] {
			results[i] = models.Scene{ID: id, SiteID: slug}
			continue
		}
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					// Leaving results[idx] unwritten is deliberate: the zero value has an
					// empty ID and the consumer below skips those, so a cancelled fetch
					// contributes no scene. Do not "fix" this by writing a stub here —
					// a populated slot is emitted as a real scene with no title or URL.
					return
				}
			}

			body, err := s.fetchPage(ctx, base+"/video/"+id)
			if err != nil {
				scraper.Debugf(1, "nookies: %s detail %s failed: %v (skipping)", slug, id, err)
				return
			}
			if sc, ok := newScene(body, id, slug, base, studioURL, now); ok {
				results[idx] = sc
			}
		}(i, id)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, sc := range results {
		if sc.ID == "" { // failed fetch
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func newScene(body []byte, id, slug, base, studioURL string, now time.Time) (models.Scene, bool) {
	vo := parseutil.ExtractVideoObject(body)
	if vo == nil || vo.Name == "" {
		return models.Scene{}, false
	}
	scene := models.Scene{
		ID:          id,
		SiteID:      slug,
		StudioURL:   studioURL,
		Title:       cleanText(vo.Name),
		URL:         base + "/video/" + id,
		Description: cleanText(vo.Description),
		Thumbnail:   vo.ThumbnailURL,
		Preview:     vo.ContentURL,
		Performers:  cleanAll(vo.Actors),
		Studio:      studioNames[slug],
		Tags:        sceneTags(body),
		Duration:    parseutil.ParseDurationISO(vo.Duration),
		ScrapedAt:   now,
	}
	date := strings.TrimSpace(vo.UploadDate)
	if date == "" {
		date = strings.TrimSpace(vo.DatePublished)
	}
	if t, err := parseutil.TryParseDate(date, time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02"); err == nil {
		scene.Date = t.UTC()
	}
	return scene, true
}

// sceneTags returns the scene's tags, preferring the VideoObject "genre"
// array and falling back to the page's visible tag pills when genre is
// absent (some brands, e.g. over40handjobs, only populate genre on some
// scenes).
func sceneTags(body []byte) []string {
	if tags := parseGenre(body); len(tags) > 0 {
		return tags
	}
	return parseTagPills(body)
}

// parseGenre pulls the VideoObject "genre" array (scene tags) from the body.
func parseGenre(body []byte) []string {
	m := genreBlockRe.FindSubmatch(body)
	if m == nil {
		return nil
	}
	var tags []string
	for _, q := range quotedRe.FindAllSubmatch(m[1], -1) {
		if t := cleanText(string(q[1])); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// parseTagPills scrapes the visible tag pills (`<a href=".../tag/{slug}">Label</a>`)
// from a detail page, deduped in document order.
func parseTagPills(body []byte) []string {
	seen := make(map[string]bool)
	var tags []string
	for _, m := range tagPillRe.FindAllSubmatch(body, -1) {
		tag := cleanText(string(m[1]))
		if tag != "" && !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	return tags
}

func cleanText(s string) string {
	return html.UnescapeString(strings.TrimSpace(s))
}

func cleanAll(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if c := cleanText(s); c != "" {
			out = append(out, c)
		}
	}
	return out
}
