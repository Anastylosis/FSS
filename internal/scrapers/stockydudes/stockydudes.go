// Package stockydudes scrapes stockydudes.com, a custom PHP CMS. A listing
// page carries its first 24 cards and a `#scenesLoadMore` element naming the
// parameters for `/_ajaxLoadScenes.php`, which serves the rest as JSON-wrapped
// HTML; the detail page adds the description and categories.
// See docs/scrapers.md.
package stockydudes

import (
	"context"
	"encoding/json"
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

const (
	siteID     = "stockydudes"
	studioName = "Stocky Dudes"
	defaultURL = "https://www.stockydudes.com"
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: defaultURL}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"stockydudes.com",
		"stockydudes.com/scenes",
		"stockydudes.com/scenes/category/{id-slug}",
		"stockydudes.com/profile/{id-slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?stockydudes\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// listingPath maps a studio URL to the listing page that carries its cards.
// The bare site and `/scenes` are the whole catalogue; a category or a
// profile page is the same card list narrowed by the site.
func listingPath(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "/scenes"
	}
	p := strings.TrimRight(u.Path, "/")
	if strings.HasPrefix(p, "/scenes/category/") || strings.HasPrefix(p, "/profile/") {
		return p
	}
	return "/scenes"
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	path := listingPath(studioURL)
	scraper.Debugf(1, "%s: scraping listing %s", siteID, path)

	items, ok := s.collect(ctx, path, opts, out)
	if !ok || ctx.Err() != nil || len(items) == 0 {
		return
	}
	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	workers := scraper.WorkerCount(opts, 4)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)

	work := make(chan item, workers)
	var wg sync.WaitGroup
	now := time.Now().UTC()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				body, err := s.get(ctx, s.base+"/scene/"+it.slug, nil)
				if err != nil {
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", it.slug, err)):
					case <-ctx.Done():
						return
					}
				} else {
					it.applyDetail(string(body))
				}
				select {
				case out <- scraper.Scene(it.toScene(s.base, studioURL, now)):
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	defer wg.Wait()
	defer close(work)
	for _, it := range items {
		select {
		case work <- it:
		case <-ctx.Done():
			return
		}
	}
}

// collect walks the listing, newest first, stopping at the end of the page
// that holds a known scene.
func (s *Scraper) collect(ctx context.Context, path string, opts scraper.ListOpts, out chan<- scraper.SceneResult) ([]item, bool) {
	fail := func(err error) ([]item, bool) {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return nil, false
	}

	body, err := s.get(ctx, s.base+path, nil)
	if err != nil {
		return fail(err)
	}
	page := string(body)
	var items []item
	seen := map[string]bool{}
	stop := false

	take := func(cards []item) {
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			if opts.KnownIDs[c.id] {
				stop = true
				continue
			}
			items = append(items, c)
		}
	}
	take(parseCards(page))

	more := parseLoadMore(page)
	for from := more.from; more.ok && !stop; from++ {
		if ctx.Err() != nil {
			return items, false
		}
		if !scraper.Pace(ctx, opts.Delay) {
			return items, false
		}
		raw := "context=" + url.QueryEscape(more.context) + "&filter=" + more.filter + "&sort=" + url.QueryEscape(more.sort) + "&from=" + strconv.Itoa(from)
		scraper.Debugf(1, "%s: fetching listing chunk %d", siteID, from)
		resp, err := s.get(ctx, s.base+"/_ajaxLoadScenes.php?"+raw, map[string]string{"X-Requested-With": "XMLHttpRequest"})
		if err != nil {
			return fail(fmt.Errorf("chunk %d: %w", from, err))
		}
		chunk, err := parseChunk(resp)
		if err != nil {
			return fail(scraper.ParseError(fmt.Sprintf("chunk %d", from), err))
		}
		cards := parseCards(chunk.HTML)
		if len(cards) == 0 {
			break
		}
		take(cards)
		if chunk.ShowMore != "yes" {
			break
		}
	}
	if stop {
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
	return items, true
}

func (s *Scraper) get(ctx context.Context, u string, extra map[string]string) ([]byte, error) {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	for k, v := range extra {
		h[k] = v
	}
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: h})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

var (
	cardSplitRe = regexp.MustCompile(`class="[^"]*\bsceneBorder"`)
	linkRe      = regexp.MustCompile(`href="/scene/(\d+)-([^"]+)"`)
	titleRe     = regexp.MustCompile(`(?s)wrapperSceneTitle">\s*<a[^>]*>(.*?)</a>`)
	posterRe    = regexp.MustCompile(`poster="([^"]+)"`)
	minutesRe   = regexp.MustCompile(`icon-clock-1"></i>\s*(\d+)\s*min`)
	dateRe      = regexp.MustCompile(`dateLbl">\s*([^<]+?)\s*<`)
	castRe      = regexp.MustCompile(`(?s)<h4>(.*?)</h4>`)
	anchorRe    = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	descRe      = regexp.MustCompile(`(?s)<div class="p-5">\s*<p>(.*?)</p>`)
	catsRe      = regexp.MustCompile(`(?s)Categories:(.*?)</h5>`)
	ogImageRe   = regexp.MustCompile(`<meta property="og:image"\s+content="([^"]+)"`)
	tagStripRe  = regexp.MustCompile(`<[^>]*>`)
	loadMoreRe  = regexp.MustCompile(`(?s)id="scenesLoadMore"(.*?)>`)
	attrRe      = regexp.MustCompile(`data-(context|from|filter|sort)="([^"]*)"`)
)

type item struct {
	id, slug, title, thumb string
	performers             []string
	date                   time.Time
	duration               int
	description            string
	categories             []string
}

func parseCards(page string) []item {
	locs := cardSplitRe.FindAllStringIndex(page, -1)
	var items []item
	for i, loc := range locs {
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[loc[0]:end]
		lm := linkRe.FindStringSubmatch(block)
		tm := titleRe.FindStringSubmatch(block)
		if lm == nil || tm == nil {
			continue
		}
		it := item{id: lm[1], slug: lm[1] + "-" + lm[2], title: clean(tm[1])}
		if m := posterRe.FindStringSubmatch(block); m != nil {
			it.thumb = html.UnescapeString(m[1])
		}
		if m := minutesRe.FindStringSubmatch(block); m != nil {
			n, _ := strconv.Atoi(m[1])
			it.duration = n * 60
		}
		if m := dateRe.FindStringSubmatch(block); m != nil {
			if t, err := parseutil.TryParseDate(m[1], "Jan 2, 2006"); err == nil {
				it.date = t.UTC()
			}
		}
		if m := castRe.FindStringSubmatch(block); m != nil {
			it.performers = anchorTexts(m[1])
		}
		if it.title != "" {
			items = append(items, it)
		}
	}
	return items
}

func (it *item) applyDetail(page string) {
	if m := descRe.FindStringSubmatch(page); m != nil {
		it.description = clean(m[1])
	}
	if m := catsRe.FindStringSubmatch(page); m != nil {
		it.categories = anchorTexts(m[1])
	}
	if m := ogImageRe.FindStringSubmatch(page); m != nil {
		it.thumb = html.UnescapeString(m[1])
	}
}

func (it item) toScene(base, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:          it.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Studio:      studioName,
		Title:       it.title,
		URL:         base + "/scene/" + it.slug,
		Description: it.description,
		Thumbnail:   it.thumb,
		Performers:  it.performers,
		Categories:  it.categories,
		Date:        it.date,
		Duration:    it.duration,
		ScrapedAt:   now,
	}
}

type loadMore struct {
	ok                    bool
	from                  int
	context, filter, sort string
}

func parseLoadMore(page string) loadMore {
	m := loadMoreRe.FindStringSubmatch(page)
	if m == nil {
		return loadMore{}
	}
	lm := loadMore{from: 2}
	for _, a := range attrRe.FindAllStringSubmatch(m[1], -1) {
		switch a[1] {
		case "context":
			lm.context = a[2]
		case "filter":
			lm.filter = a[2]
		case "sort":
			lm.sort = a[2]
		case "from":
			if n, err := strconv.Atoi(a[2]); err == nil {
				lm.from = n
			}
		}
	}
	lm.ok = lm.filter != "" && lm.sort != ""
	return lm
}

func anchorTexts(s string) []string {
	var out []string
	for _, a := range anchorRe.FindAllStringSubmatch(s, -1) {
		if t := clean(a[1]); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

type chunk struct {
	Status   string `json:"status"`
	HTML     string `json:"html"`
	ShowMore string `json:"showMore"`
	Text     string `json:"text"`
}

func parseChunk(body []byte) (chunk, error) {
	var c chunk
	if err := json.Unmarshal(body, &c); err != nil {
		return c, err
	}
	if c.Status != "ok" {
		return c, fmt.Errorf("listing endpoint answered %q: %s", c.Status, c.Text)
	}
	return c, nil
}
