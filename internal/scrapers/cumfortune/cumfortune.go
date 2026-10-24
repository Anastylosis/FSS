package cumfortune

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
	"time"
	"unicode/utf8"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID      = "cumfortune"
	studioName  = "CumFortune"
	defaultBase = "https://cumfortune.com"
	indexPath   = "/Scenes_Start.htm"
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: defaultBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"cumfortune.com",
		"cumfortune.com/Scenes_Start.htm",
		"cumfortune.com/Promos/AAA_GIRLS/{model}",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?cumfortune\.com(?:/(?:index\.html?|models\.htm|Scenes_Start\.htm|Promos/AAA_GIRLS/[\w.-]+\.html?)?)?/?(?:[?#].*)?$`)
	modelRe = regexp.MustCompile(`/Promos/AAA_GIRLS/[\w.-]+\.html?$`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	if u, err := url.Parse(studioURL); err == nil && modelRe.MatchString(u.Path) {
		scraper.Debugf(1, "cumfortune: scraping model page %s", u.Path)
		s.runModel(ctx, studioURL, s.base+u.Path, now, out)
		return
	}

	indexURL := s.base + indexPath
	body, err := s.fetch(ctx, indexURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	pages := parsePageLinks(body, indexURL)
	if len(pages) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(indexURL, errors.New("no scene page links found"))))
		return
	}
	scraper.Debugf(1, "cumfortune: %d listing pages, walking newest first", len(pages))

	// The pages are numbered oldest to newest and only the last one grows,
	// so walking them in reverse puts new scenes first for KnownIDs.
	seen := map[string]bool{}
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := pages[len(pages)-page]
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := parseScenes(body, pageURL)
		if len(items) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, errors.New("no scenes on listing page"))
		}
		var scenes []models.Scene
		for _, it := range items {
			sc := toScene(studioURL, it, now)
			if seen[sc.ID] {
				continue
			}
			seen[sc.ID] = true
			scenes = append(scenes, sc)
		}
		return scraper.PageResult{Scenes: scenes, Done: page >= len(pages), Continue: true}, nil
	})
}

func (s *Scraper) runModel(ctx context.Context, studioURL, pageURL string, now time.Time, out chan<- scraper.SceneResult) {
	body, err := s.fetch(ctx, pageURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	items := parseScenes(body, pageURL)
	if len(items) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(pageURL, errors.New("no scenes on model page"))))
		return
	}
	seen := map[string]bool{}
	var scenes []models.Scene
	for _, it := range items {
		sc := toScene(studioURL, it, now)
		if !seen[sc.ID] {
			seen[sc.ID] = true
			scenes = append(scenes, sc)
		}
	}
	if !send(ctx, out, scraper.Progress(len(scenes))) {
		return
	}
	for _, sc := range scenes {
		if !send(ctx, out, scraper.Scene(sc)) {
			return
		}
	}
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scraper) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rawURL, err)
	}
	return latin1ToUTF8(body), nil
}

// latin1ToUTF8 converts the site's declared iso-8859-1 to UTF-8; bodies
// that are already valid UTF-8 (in practice: plain ASCII) pass through.
func latin1ToUTF8(b []byte) []byte {
	if utf8.Valid(b) {
		return b
	}
	out := make([]rune, len(b))
	for i, c := range b {
		out[i] = rune(c)
	}
	return []byte(string(out))
}

// ---- parsing ----

var (
	pageLinkRe  = regexp.MustCompile(`class="Name"><a href="([^"]*ZZZZ_Scenes/Scenes_[^"]+\.html?)"`)
	blockStart  = regexp.MustCompile(`<td colspan="3" class="Actor">`)
	titleRe     = regexp.MustCompile(`(?s)^(.*?)</td>`)
	modelCellRe = regexp.MustCompile(`(?s)Model:</td>\s*<td[^>]*>(.*?)</td>`)
	tagBtnRe    = regexp.MustCompile(`Buttons/(\w+)\.png`)
	thumbRe     = regexp.MustCompile(`(?s)rowspan="5"[^>]*>\s*(?:<[^>]*>\s*)*?<img[^>]*src="([^"]+)"`)
	descRe      = regexp.MustCompile(`(?s)rowspan="3"[^>]*>\s*<p class="TXT">(.*?)</p>`)
	durationRe  = regexp.MustCompile(`(\d+)\s*min\b`)
	playRe      = regexp.MustCompile(`href="([^"]*Members/([^"]+?)\.html?)"[^>]*>\s*<img[^>]*play\.png`)
	tagStripRe  = regexp.MustCompile(`<[^>]*>`)
	spaceRe     = regexp.MustCompile(`\s+`)
	slugRe      = regexp.MustCompile(`[^a-z0-9]+`)
	perfSplitRe = regexp.MustCompile(`\s*[&,]\s*`)
)

// tagNames maps the category button images to the label drawn on them.
var tagNames = map[string]string{
	"Anal":       "Anal",
	"BGG":        "BGG",
	"BJ":         "Blowjob",
	"Cuminmouth": "Cum in Mouth",
	"Cumonass":   "Cum on Ass",
	"CumSwap":    "Cum Swap",
	"Deepthroat": "Deepthroat",
	"Dildo":      "Dildo",
	"Facial":     "Facial",
	"FAC":        "Fuck after Cumshot",
	"FemOrg":     "Female Orgasm",
	"Fetish":     "Fetish",
	"Smoking":    "Smoking",
	"Suqirt":     "Squirt",
	"Swallow":    "Swallow",
}

type item struct {
	memberPath  string // e.g. "Bella/index_22"
	url         string
	title       string
	performers  []string
	tags        []string
	thumbnail   string
	description string
	duration    int
}

func parsePageLinks(body []byte, base string) []string {
	var pages []string
	seen := map[string]bool{}
	for _, m := range pageLinkRe.FindAllSubmatch(body, -1) {
		abs := resolve(base, string(m[1]))
		if abs == "" || seen[abs] {
			continue
		}
		seen[abs] = true
		pages = append(pages, abs)
	}
	return pages
}

func parseScenes(body []byte, pageURL string) []item {
	locs := blockStart.FindAllIndex(body, -1)
	items := make([]item, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if it, ok := parseBlock(body[loc[1]:end], pageURL); ok {
			items = append(items, it)
		}
	}
	return items
}

func parseBlock(b []byte, pageURL string) (item, bool) {
	pm := playRe.FindSubmatch(b)
	tm := titleRe.FindSubmatch(b)
	if pm == nil || tm == nil {
		return item{}, false
	}
	it := item{
		memberPath: string(pm[2]),
		url:        resolve(pageURL, string(pm[1])),
		title:      cleanText(string(tm[1])),
	}
	if it.title == "" {
		return item{}, false
	}
	if m := modelCellRe.FindSubmatch(b); m != nil {
		for _, p := range perfSplitRe.Split(cleanText(string(m[1])), -1) {
			if p = strings.TrimSpace(p); p != "" {
				it.performers = append(it.performers, p)
			}
		}
	}
	seenTag := map[string]bool{}
	for _, m := range tagBtnRe.FindAllSubmatch(b, -1) {
		key := string(m[1])
		if key == "play" {
			continue
		}
		name, ok := tagNames[key]
		if !ok {
			name = key
		}
		if !seenTag[name] {
			seenTag[name] = true
			it.tags = append(it.tags, name)
		}
	}
	if m := thumbRe.FindSubmatch(b); m != nil {
		it.thumbnail = resolve(pageURL, string(m[1]))
	}
	if m := descRe.FindSubmatch(b); m != nil {
		it.description = cleanText(string(m[1]))
	}
	if m := durationRe.FindSubmatch(b); m != nil {
		if n, err := strconv.Atoi(string(m[1])); err == nil {
			it.duration = n * 60
		}
	}
	return it, true
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

func resolve(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	r, err := url.Parse(strings.ReplaceAll(ref, " ", "%20"))
	if err != nil {
		return ""
	}
	return b.ResolveReference(r).String()
}

// sceneID keys on the members-area path plus the title: the play link is
// the only per-scene identifier the tour carries, and a handful of
// listing entries reuse another scene's link by copy-paste.
func sceneID(it item) string {
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(it.title), "-"), "-")
	return strings.ToLower(it.memberPath) + "/" + slug
}

func toScene(studioURL string, it item, now time.Time) models.Scene {
	return models.Scene{
		ID:          sceneID(it),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       it.title,
		URL:         it.url,
		Thumbnail:   it.thumbnail,
		Description: it.description,
		Duration:    it.duration,
		Performers:  it.performers,
		Tags:        it.tags,
		Studio:      studioName,
		ScrapedAt:   now,
	}
}
