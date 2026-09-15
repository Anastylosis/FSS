package belamionline

import (
	"context"
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

const siteID = "belamionline"

const tourBase = "https://newtour.belamionline.com"

// perPage is the listing page size the tour renders.
const perPage = 32

type section struct {
	page string
	name string
}

var sections = []section{
	{"latestsexscenes.aspx", "scenes"},
	{"latestsolos.aspx", "solos"},
	{"latestvintage.aspx", "vintage"},
	{"latestbackstage.aspx", "backstage"},
}

type Scraper struct {
	Client *http.Client
	// base is the tour origin, overridable so the listing and model paths can
	// be exercised offline.
	base string
}

func New() *Scraper {
	return &Scraper{
		Client: httpx.NewClient(30 * time.Second),
		base:   tourBase,
	}
}

func (s *Scraper) origin() string {
	if s.base == "" {
		return tourBase
	}
	return s.base
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"belamionline.com",
		"belamionline.com/latestsexscenes.aspx",
		"belamionline.com/latestsolos.aspx",
		"belamionline.com/latestvintage.aspx",
		"belamionline.com/latestbackstage.aspx",
		"belamionline.com/modelsindex.aspx?ModelID={id}",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:(?:www|newtour)\.)?belamionline\.com(?:/|$)`)
	modelRe = regexp.MustCompile(`[?&]ModelID=(\d+)`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if m := modelRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "belamionline: scraping model page (ModelID=%s)", m[1])
		s.runModel(ctx, studioURL, opts, out)
		return
	}

	sec := detectSection(studioURL)
	if sec != nil {
		scraper.Debugf(1, "belamionline: scraping section %s", sec.name)
		s.runSection(ctx, studioURL, opts, out, *sec)
		return
	}

	scraper.Debugf(1, "belamionline: scraping all sections")
	for _, sec := range sections {
		if ctx.Err() != nil {
			return
		}
		scraper.Debugf(1, "belamionline: starting section %s", sec.name)
		s.runSection(ctx, studioURL, opts, out, sec)
	}
}

func detectSection(u string) *section {
	lower := strings.ToLower(u)
	for _, sec := range sections {
		if strings.Contains(lower, strings.ToLower(sec.page)) {
			return &sec
		}
	}
	return nil
}

func (s *Scraper) runSection(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, sec section) {
	now := time.Now().UTC()
	maxPage := 0
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.origin() + "/" + sec.page
		if page > 1 {
			pageURL += "?page=" + strconv.Itoa(page)
		}
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := parseListingPage(body)
		total := 0
		// Re-read on every page: the pager is windowed, so a later page can
		// link further than the first one did.
		if seen := parseMaxPage(body); seen > maxPage {
			maxPage = seen
		}
		if page == 1 {
			total = maxPage * perPage
		}
		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = toScene(studioURL, item, now)
		}
		// Termination comes from the pager the site itself renders. The
		// short-page check is only a fallback: a page-size change would make a
		// hardcoded count stop the walk on the very first page.
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   (maxPage > 0 && page >= maxPage) || len(items) < perPage,
		}, nil
	})
}

func (s *Scraper) runModel(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	m := modelRe.FindStringSubmatch(studioURL)
	if m == nil {
		return
	}
	pageURL := s.origin() + "/modelsindex.aspx?ModelID=" + m[1]
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

	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	// The model page is a single fetch that is already fully parsed, so
	// stopping at a known scene saves no request and drops the rest of the
	// model's catalogue. Skip and continue.
	now := time.Now().UTC()
	skipped := 0
	for _, item := range items {
		scene := toScene(studioURL, item, now)
		if opts.KnownIDs[scene.ID] {
			skipped++
			continue
		}
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}

	if skipped > 0 {
		scraper.Debugf(1, "belamionline: skipped %d already-stored scene(s)", skipped)
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     url,
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
	videoID     string
	title       string
	description string
	thumbnail   string
	date        time.Time
	tags        []string
}

var (
	contentStartRe = regexp.MustCompile(`<div class="content">`)
	videoIDRe      = regexp.MustCompile(`playvideo\.aspx\?VideoID=(\d+)`)
	labelRe        = regexp.MustCompile(`(?s)<span class="label">(.*?)</span>`)
	dataSrcRe      = regexp.MustCompile(`data-src="(https://[^"]+)"`)
	altRe          = regexp.MustCompile(`alt="([^"]*)"`)
	dateRe         = regexp.MustCompile(`<div class="date">([\d/]+)</div>`)
	tagRe          = regexp.MustCompile(`(?s)<div class="tags">(.*?)</div>`)
	tagLinkRe      = regexp.MustCompile(`>([^<]+)</a>`)
	maxPageRe      = regexp.MustCompile(`(?s)class="pag_b">(.*?)</div>`)
	pageNumRe      = regexp.MustCompile(`page=(\d+)`)
)

func parseListingPage(body []byte) []listItem {
	page := string(body)
	locs := contentStartRe.FindAllStringIndex(page, -1)
	items := make([]listItem, 0, len(locs))

	for i, loc := range locs {
		start := loc[0]
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[start:end]
		if item, ok := parseBlock(block); ok {
			items = append(items, item)
		}
	}
	return items
}

func parseBlock(block string) (listItem, bool) {
	m := videoIDRe.FindStringSubmatch(block)
	if m == nil {
		return listItem{}, false
	}
	item := listItem{videoID: m[1]}

	if mLabel := labelRe.FindStringSubmatch(block); mLabel != nil {
		item.title = strings.TrimSpace(html.UnescapeString(mLabel[1]))
	}

	if mSrc := dataSrcRe.FindStringSubmatch(block); mSrc != nil {
		item.thumbnail = mSrc[1]
	}

	if mAlt := altRe.FindStringSubmatch(block); mAlt != nil {
		desc := strings.TrimSpace(html.UnescapeString(mAlt[1]))
		if len(desc) > 10 {
			item.description = desc
		}
	}

	if mDate := dateRe.FindStringSubmatch(block); mDate != nil {
		if t, err := time.Parse("1/2/2006", mDate[1]); err == nil {
			item.date = t.UTC()
		}
	}

	if mTags := tagRe.FindStringSubmatch(block); mTags != nil {
		for _, tm := range tagLinkRe.FindAllStringSubmatch(mTags[1], -1) {
			tag := strings.TrimSpace(html.UnescapeString(tm[1]))
			if tag != "" {
				item.tags = append(item.tags, tag)
			}
		}
	}

	return item, true
}

// parseMaxPage reads the highest page the pager links to, or 0 when there is
// no pager to read. Zero means "unknown" — returning 1 instead made a markup
// change stop the walk after the first page. See docs/scrapers.md.
func parseMaxPage(body []byte) int {
	m := maxPageRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	maxPage := 0
	for _, pm := range pageNumRe.FindAllSubmatch(m[1], -1) {
		n, _ := strconv.Atoi(string(pm[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

func parsePerformers(title string) []string {
	if title == "" {
		return nil
	}
	// Titles like "Private shots - ORGY 1" or "Blond Bottoms Orgy" don't have performer names.
	if strings.Contains(strings.ToLower(title), "orgy") ||
		strings.Contains(strings.ToLower(title), "private shots") ||
		strings.Contains(title, " - Part ") {
		return nil
	}
	parts := strings.Split(title, " & ")
	var performers []string
	for _, p := range parts {
		for _, name := range strings.Split(p, ", ") {
			name = strings.TrimSpace(name)
			if name != "" {
				performers = append(performers, name)
			}
		}
	}
	return performers
}

func toScene(studioURL string, item listItem, now time.Time) models.Scene {
	return models.Scene{
		ID:          item.videoID,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         tourBase + "/playvideo.aspx?VideoID=" + item.videoID,
		Date:        item.date,
		Description: item.description,
		Thumbnail:   item.thumbnail,
		Performers:  parsePerformers(item.title),
		Tags:        item.tags,
		Studio:      "BelAmi",
		ScrapedAt:   now,
	}
}
