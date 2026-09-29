package data18

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
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
	siteBase = "https://www.data18.com"
	perPage  = 30
)

type Scraper struct {
	client *http.Client
	// base is the site root. Overridable so offline tests can point the
	// listing, captcha and detail fetches at one test server — with a const
	// they could not, which is why the end-to-end test was a stub.
	base string
}

func New() *Scraper {
	jar, _ := cookiejar.New(nil)
	c := httpx.NewClient(30 * time.Second)
	c.Jar = jar
	return &Scraper{client: c, base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "data18" }

func (s *Scraper) Patterns() []string {
	return []string{
		"data18.com/studios/{slug}",
		"data18.com/studios/{slug}/{sub}",
		"data18.com/name/{slug}",
		"data18.com/tags/{slug}",
	}
}

var urlMatchRe = regexp.MustCompile(`^https?://(?:www\.)?data18\.com/(studios|name|tags)/`)

func (s *Scraper) MatchesURL(u string) bool {
	return urlMatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listingKind int

const (
	kindPerformer listingKind = 2
	kindStudio    listingKind = 3
	kindTag       listingKind = 4
)

type listingConfig struct {
	kind  listingKind
	slug  string
	slug2 string
}

var (
	studioSubRe  = regexp.MustCompile(`data18\.com/studios/([^/?#]+)/([^/?#]+)`)
	studioPathRe = regexp.MustCompile(`data18\.com/studios/([^/?#]+)`)
	namePathRe   = regexp.MustCompile(`data18\.com/name/([^/?#]+)`)
	tagPathRe    = regexp.MustCompile(`data18\.com/tags/([^/?#]+)`)
)

func classifyURL(rawURL string) listingConfig {
	if m := studioSubRe.FindStringSubmatch(rawURL); m != nil {
		return listingConfig{kind: kindStudio, slug: m[1], slug2: m[2]}
	}
	if m := studioPathRe.FindStringSubmatch(rawURL); m != nil {
		return listingConfig{kind: kindStudio, slug: m[1]}
	}
	if m := namePathRe.FindStringSubmatch(rawURL); m != nil {
		return listingConfig{kind: kindPerformer, slug: m[1]}
	}
	if m := tagPathRe.FindStringSubmatch(rawURL); m != nil {
		return listingConfig{kind: kindTag, slug: m[1]}
	}
	return listingConfig{kind: kindStudio}
}

func (lc listingConfig) ajaxURL(base string, page int) string {
	return fmt.Sprintf("%s/sys/page.php?t=%d&b=1&o=0&html=%s&html2=%s&total=&doquery=1&spage=%d&dopage=1",
		base, lc.kind, lc.slug, lc.slug2, page)
}

func (s *Scraper) bootstrap(ctx context.Context) error {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     s.base + "/sys/captcha",
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return fmt.Errorf("captcha bootstrap: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}

type listEntry struct {
	id         string
	title      string
	url        string
	date       string
	thumbnail  string
	performers []string
	studio     string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if err := s.bootstrap(ctx); err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	lc := classifyURL(studioURL)

	// Fetch the initial page to seed cookies and get page 1 scenes.
	initialBody, err := s.fetchPage(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("initial page: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	work := make(chan listEntry, opts.Workers)
	var wg sync.WaitGroup

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, entry, studioURL)
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

	// Page 1: parse from initial page load.
	entries := parseSceneCards(s.base, initialBody)
	total := extractTotal(initialBody)
	if total == 0 {
		total = len(entries)
	}
	if total > 0 {
		scraper.Debugf(1, "data18: %d total scenes", total)
		select {
		case out <- scraper.Progress(total):
		case <-ctx.Done():
		}
	}

	maxPage := 0
	if total > 0 {
		maxPage = (total + perPage - 1) / perPage
	}
	if mp := extractMaxPage(initialBody); mp > 0 {
		maxPage = mp
	}

	if !s.sendEntries(ctx, entries, opts, work, out) {
		close(work)
		wg.Wait()
		return
	}

	// Pages 2+: fetch via AJAX endpoint.
	for page := 2; page <= maxPage || maxPage == 0; page++ {
		if ctx.Err() != nil {
			break
		}
		if !scraper.Pace(ctx, opts.Delay) {
			break
		}
		scraper.Debugf(1, "data18: fetching page %d", page)

		body, err := s.fetchAjax(ctx, lc.ajaxURL(s.base, page), studioURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		entries := parseSceneCards(s.base, body)
		if len(entries) == 0 {
			break
		}

		if mp := extractMaxPage(body); mp > 0 {
			maxPage = mp
		}

		if !s.sendEntries(ctx, entries, opts, work, out) {
			break
		}

		if len(entries) < perPage {
			break
		}
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) sendEntries(ctx context.Context, entries []listEntry, opts scraper.ListOpts, work chan<- listEntry, out chan<- scraper.SceneResult) bool {
	for _, e := range entries {
		if opts.KnownIDs[e.id] {
			scraper.Debugf(1, "data18: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return false
		}
		select {
		case work <- e:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// Listing page parsing.
var (
	totalRe   = regexp.MustCompile(`<b>([\d,]+)\s+Scenes?</b>`)
	maxPageRe = regexp.MustCompile(`class="spagemanual"[^>]*\bmax="(\d+)"`)
	itemDivRe = regexp.MustCompile(`<div\s+id="item\d+"`)

	sceneURLRe       = regexp.MustCompile(`href="(?:https?://(?:www\.)?data18\.com)?/scenes/(\d+)`)
	sceneTitleRe     = regexp.MustCompile(`class="gen12[^"]*bold[^"]*"[^>]*>\s*([^<]+?)\s*</a>`)
	dateTextRe       = regexp.MustCompile(`((?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+\d{1,2},?\s+\d{4})`)
	thumbDataSrcRe   = regexp.MustCompile(`data-src="(https?://cdn\.dt18\.com/[^"]+)"`)
	thumbSrcRe       = regexp.MustCompile(`src="(https?://cdn\.dt18\.com/media/[^"]+)"`)
	performerLinkRe  = regexp.MustCompile(`<a\s+href="(?:https?://(?:www\.)?data18\.com)?/name/[^"]*"[^>]*>([^<]+)</a>`)
	listStudioLinkRe = regexp.MustCompile(`(?:Webserie|Site):\s*<a\s+href="(?:https?://(?:www\.)?data18\.com)?/studios/[^"]*"[^>]*>([^<]+)</a>`)
)

func parseSceneCards(base string, body []byte) []listEntry {
	s := string(body)
	locs := itemDivRe.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return nil
	}

	entries := make([]listEntry, 0, len(locs))
	seen := make(map[string]bool)

	for i, loc := range locs {
		start := loc[0]
		end := len(s)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := s[start:end]

		idMatch := sceneURLRe.FindStringSubmatch(block)
		if idMatch == nil {
			continue
		}
		id := idMatch[1]
		if seen[id] {
			continue
		}
		seen[id] = true

		e := listEntry{
			id:  id,
			url: fmt.Sprintf("%s/scenes/%s", base, id),
		}

		if m := sceneTitleRe.FindStringSubmatch(block); m != nil {
			e.title = strings.TrimSpace(html.UnescapeString(m[1]))
		}

		if m := dateTextRe.FindStringSubmatch(block); m != nil {
			e.date = m[1]
		}

		if m := thumbDataSrcRe.FindStringSubmatch(block); m != nil {
			e.thumbnail = m[1]
		} else if m := thumbSrcRe.FindStringSubmatch(block); m != nil {
			e.thumbnail = m[1]
		}

		perfSeen := map[string]bool{}
		for _, m := range performerLinkRe.FindAllStringSubmatch(block, -1) {
			name := normalizeName(m[1])
			if name != "" && !perfSeen[name] {
				perfSeen[name] = true
				e.performers = append(e.performers, name)
			}
		}

		if m := listStudioLinkRe.FindStringSubmatch(block); m != nil {
			e.studio = normalizeName(m[1])
		}

		entries = append(entries, e)
	}

	return entries
}

func extractTotal(body []byte) int {
	m := totalRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	s := strings.ReplaceAll(string(m[1]), ",", "")
	n, _ := strconv.Atoi(s)
	return n
}

func extractMaxPage(body []byte) int {
	m := maxPageRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// Detail page parsing.
var (
	detailDurationRe  = regexp.MustCompile(`Duration:\s*<b>\s*(\d+)\s*min(?:,?\s*(\d+)\s*sec)?`)
	detailDescRe      = regexp.MustCompile(`(?s)class="[^"]*boxdesc[^"]*">\s*(.*?)\s*</div>`)
	detailTagRe       = regexp.MustCompile(`<a\s+href="(?:https?://(?:www\.)?data18\.com)?/tags/[^"]*"[^>]*>([^<]+)</a>`)
	detailPerformerRe = regexp.MustCompile(`<a\s+href="(?:https?://(?:www\.)?data18\.com)?/name/[^"]*"[^>]*class="[^"]*bold[^"]*"[^>]*>([^<]+)</a>`)
	detailStudioRe    = regexp.MustCompile(`<b>Studio</b>:\s*<a[^>]*>([^<]+)</a>`)
	detailDateFullRe  = regexp.MustCompile(`Release date</b>:\s*((?:January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{1,2},?\s+\d{4})`)
	detailDateMonthRe = regexp.MustCompile(`Release date</b>:\s*((?:January|February|March|April|May|June|July|August|September|October|November|December),?\s+\d{4})`)
	stripTagsRe       = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) fetchDetail(ctx context.Context, entry listEntry, studioURL string) (models.Scene, error) {
	body, err := s.fetchPage(ctx, entry.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", entry.id, err)
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:         entry.id,
		SiteID:     "data18",
		StudioURL:  studioURL,
		Title:      entry.title,
		URL:        entry.url,
		Thumbnail:  entry.thumbnail,
		Performers: entry.performers,
		Studio:     entry.studio,
		ScrapedAt:  now,
	}

	if entry.date != "" {
		scene.Date = parseDate(entry.date)
	}

	if m := detailDurationRe.FindSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(string(m[1]))
		secs := 0
		if len(m) > 2 && len(m[2]) > 0 {
			secs, _ = strconv.Atoi(string(m[2]))
		}
		scene.Duration = mins*60 + secs
	}

	if m := detailDescRe.FindSubmatch(body); m != nil {
		desc := stripTagsRe.ReplaceAll(m[1], []byte(" "))
		scene.Description = strings.Join(strings.Fields(html.UnescapeString(string(desc))), " ")
	}

	if idx := strings.Index(string(body), "Categories:"); idx >= 0 {
		section := string(body[idx:])
		if endIdx := strings.Index(section, "</div>"); endIdx > 0 {
			section = section[:endIdx]
		}
		tagSeen := map[string]bool{}
		for _, m := range detailTagRe.FindAllStringSubmatch(section, -1) {
			tag := normalizeName(m[1])
			if tag != "" && !tagSeen[tag] {
				tagSeen[tag] = true
				scene.Tags = append(scene.Tags, tag)
			}
		}
	}

	if detailPerfs := parseDetailPerformers(body); len(detailPerfs) > 0 {
		scene.Performers = detailPerfs
	}

	if scene.Studio == "" {
		if m := detailStudioRe.FindSubmatch(body); m != nil {
			scene.Studio = strings.TrimSpace(html.UnescapeString(string(m[1])))
		}
	}

	if scene.Date.IsZero() {
		if m := detailDateFullRe.FindSubmatch(body); m != nil {
			scene.Date = parseDate(string(m[1]))
		} else if m := detailDateMonthRe.FindSubmatch(body); m != nil {
			scene.Date = parseMonthYear(string(m[1]))
		}
	}

	return scene, nil
}

func parseDetailPerformers(body []byte) []string {
	var performers []string
	seen := map[string]bool{}
	for _, m := range detailPerformerRe.FindAllSubmatch(body, -1) {
		name := normalizeName(string(m[1]))
		if name != "" && !seen[name] {
			seen[name] = true
			performers = append(performers, name)
		}
	}
	return performers
}

func normalizeName(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(html.UnescapeString(s))), " ")
}

func parseDate(s string) time.Time {
	t, _ := parseutil.TryParseDate(strings.TrimSpace(s), "January 2, 2006", "January 2 2006", "Jan 2, 2006", "Jan 2 2006")
	return t
}

func parseMonthYear(s string) time.Time {
	t, _ := parseutil.TryParseDate(strings.TrimSpace(s), "January, 2006", "January 2006", "Jan, 2006", "Jan 2006")
	return t
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

func (s *Scraper) fetchAjax(ctx context.Context, url, referer string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: url,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Accept"] = "text/html, */*; q=0.01"
			h["X-Requested-With"] = "XMLHttpRequest"
			h["Referer"] = referer
			return h
		}(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
