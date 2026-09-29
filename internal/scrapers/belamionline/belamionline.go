package belamionline

import (
	"context"
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

	rosterOnce sync.Once
	roster     map[string]bool
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
		s.runSection(ctx, studioURL, opts, out, *sec, nil)
		return
	}

	scraper.Debugf(1, "belamionline: scraping all sections")
	// Each section is its own paginated listing, so letting Paginate announce
	// each section's own total made the last one overwrite the rest and report
	// a fraction of the catalogue. The sections are summed here instead and
	// re-announced as the running total grows, which the consumer keeps
	// because a later KindTotal replaces the earlier one.
	running := 0
	for _, sec := range sections {
		if ctx.Err() != nil {
			return
		}
		scraper.Debugf(1, "belamionline: starting section %s", sec.name)
		s.runSection(ctx, studioURL, opts, out, sec, &running)
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

// runSection walks one section's listing. When running is non-nil the section's
// own total is added to it and the sum is reported, rather than each section
// announcing a total that replaces the previous section's.
func (s *Scraper) runSection(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, sec section, running *int) {
	now := time.Now().UTC()
	roster := s.loadRoster(ctx, opts.Delay)
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
			if running != nil {
				*running += total
				total = *running
			}
		}
		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = toScene(studioURL, item, now, roster)
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
	roster := s.loadRoster(ctx, opts.Delay)
	skipped := 0
	for _, item := range items {
		scene := toScene(studioURL, item, now, roster)
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

// titlePartRe splits a scene label on the separators the tour uses between
// credited boys.
var titlePartRe = regexp.MustCompile(`\s*(?:&|,)\s*`)

// parsePerformers reads the cast out of a scene label. The tour has no cast
// markup at all — the label is either a list of first names ("Dano, Julian &
// Mark") or a descriptive title ("Back to Greece Leftovers", "Lovers &
// Rivals"), and nothing in the markup tells the two apart.
//
// roster is the set of first names the site's own model index publishes,
// lowercased. A label counts as a cast list only when **every** part is a
// known model, so a descriptive title contributes nothing rather than filing
// "Lovers" and "Rivals" as performers. With no roster (the index could not be
// read) nothing is credited, which is the safe direction: a missing credit is
// recoverable, an invented one pollutes the shared performer vocabulary.
func parsePerformers(title string, roster map[string]bool) []string {
	title = strings.TrimSpace(title)
	if title == "" || len(roster) == 0 {
		return nil
	}
	// A series marker means the rest of the label is an episode, not a cast.
	if strings.Contains(title, " - ") {
		return nil
	}
	var performers []string
	seen := map[string]bool{}
	for _, part := range titlePartRe.Split(title, -1) {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if !roster[strings.ToLower(name)] {
			return nil
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		performers = append(performers, name)
	}
	return performers
}

// modelLabelRe reads a model's name off the index card.
var modelLabelRe = regexp.MustCompile(`<span class="label">([^<]*)</span>`)

// maxRosterPages bounds the model-index walk. Past the last page the tour
// clamps and re-serves it, so the walk also stops when a page adds no name.
const maxRosterPages = 60

// loadRoster walks the model index once per run and returns the set of names
// it publishes, lowercased, each under both its full spelling and its first
// name — the index lists "Alan Cartier" while scene labels credit "Alan".
func (s *Scraper) loadRoster(ctx context.Context, delay time.Duration) map[string]bool {
	s.rosterOnce.Do(func() {
		names := map[string]bool{}
		for page := 1; page <= maxRosterPages; page++ {
			if ctx.Err() != nil {
				break
			}
			if page > 1 && delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}
			pageURL := s.origin() + "/models.aspx"
			if page > 1 {
				pageURL += "?page=" + strconv.Itoa(page)
			}
			body, err := s.fetchPage(ctx, pageURL)
			if err != nil {
				scraper.Debugf(1, "%s: model index page %d: %v", siteID, page, err)
				break
			}
			added := 0
			for _, m := range modelLabelRe.FindAllStringSubmatch(string(body), -1) {
				full := strings.Join(strings.Fields(html.UnescapeString(m[1])), " ")
				if full == "" {
					continue
				}
				for _, key := range []string{strings.ToLower(full), strings.ToLower(strings.Fields(full)[0])} {
					if !names[key] {
						names[key] = true
						added++
					}
				}
			}
			if added == 0 {
				break
			}
		}
		scraper.Debugf(1, "%s: %d model names in the roster", siteID, len(names))
		s.roster = names
	})
	return s.roster
}

func toScene(studioURL string, item listItem, now time.Time, roster map[string]bool) models.Scene {
	return models.Scene{
		ID:          item.videoID,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         tourBase + "/playvideo.aspx?VideoID=" + item.videoID,
		Date:        item.date,
		Description: item.description,
		Thumbnail:   item.thumbnail,
		Performers:  parsePerformers(item.title, roster),
		Tags:        item.tags,
		Studio:      "BelAmi",
		ScrapedAt:   now,
	}
}
