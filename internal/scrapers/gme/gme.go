// Package gme scrapes gme-inc.net, the static Japanese catalogue of Global
// Media Entertainment (labels such as New MOM and Global Media Annex). The
// unfiltered listing is capped at 2,000 rows, so the whole catalogue is walked
// label by label; a label, series, director or performer URL scrapes just that
// listing. Each release's detail page adds cast, runtime and description.
// See docs/scrapers.md.
package gme

import (
	"context"
	"fmt"
	"html"
	"math"
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
	siteID     = "gme"
	studioName = "Global Media Entertainment"
	defaultURL = "http://www.gme-inc.net"
	pageSize   = 50
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
		"gme-inc.net",
		"gme-inc.net/product_list.html?label_id={id}",
		"gme-inc.net/product_list.html?series_id={id}",
		"gme-inc.net/product_list.html?director_id={id}",
		"gme-inc.net/{code}.html",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?gme-inc\.net(?:/|$)`)
	actorRe = regexp.MustCompile(`^/(ac\d+)\.html$`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

// listing is one paginated product list: a path plus a fixed query.
type listing struct {
	path  string
	query url.Values
}

func (l listing) url(base string, page int) string {
	q := url.Values{}
	for k, v := range l.query {
		q[k] = v
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	u := base + l.path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// listingFor maps a studio URL to the listing it selects; the zero listing
// (all=true) means the whole catalogue.
func listingFor(studioURL string) (l listing, all bool) {
	u, err := url.Parse(studioURL)
	if err != nil {
		return listing{}, true
	}
	q := u.Query()
	for _, key := range []string{"label_id", "series_id", "director_id"} {
		if v := q.Get(key); v != "" {
			return listing{path: "/product_list.html", query: url.Values{key: {v}}}, false
		}
	}
	if actorRe.MatchString(u.Path) {
		return listing{path: u.Path}, false
	}
	return listing{}, true
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	lists, err := s.listings(ctx, studioURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}

	var items []item
	seen := map[string]bool{}
	for _, l := range lists {
		got, err := s.walk(ctx, l, opts)
		if err != nil {
			send(ctx, out, scraper.Error(err))
		}
		for _, it := range got {
			if !seen[it.code] {
				seen[it.code] = true
				items = append(items, it)
			}
		}
		if ctx.Err() != nil {
			return
		}
	}

	var fresh []item
	for _, it := range items {
		if !opts.KnownIDs[it.code] {
			fresh = append(fresh, it)
		}
	}
	if !send(ctx, out, scraper.Progress(len(items))) {
		return
	}
	workers := scraper.WorkerCount(opts, 4)
	scraper.Debugf(1, "%s: fetching %d details with %d workers (%d already known)", siteID, len(fresh), workers, len(items)-len(fresh))

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
				body, err := s.get(ctx, s.base+"/"+it.code+".html")
				if err != nil {
					if !send(ctx, out, scraper.Error(fmt.Errorf("detail %s: %w", it.code, err))) {
						return
					}
				} else {
					it.applyDetail(string(body))
				}
				if !send(ctx, out, scraper.Scene(it.toScene(s.base, studioURL, now))) {
					return
				}
			}
		}()
	}
	defer wg.Wait()
	defer close(work)
	for _, it := range fresh {
		select {
		case work <- it:
		case <-ctx.Done():
			return
		}
	}
}

// listings returns the listings to walk: the one a filtered URL names, or
// every label of the site for the bare studio URL.
func (s *Scraper) listings(ctx context.Context, studioURL string) ([]listing, error) {
	if l, all := listingFor(studioURL); !all {
		scraper.Debugf(1, "%s: scraping listing %s", siteID, l.url("", 1))
		return []listing{l}, nil
	}
	body, err := s.get(ctx, s.base+"/product_list.html")
	if err != nil {
		return nil, err
	}
	var out []listing
	seen := map[string]bool{}
	for _, m := range labelRe.FindAllStringSubmatch(string(body), -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, listing{path: "/product_list.html", query: url.Values{"label_id": {m[1]}}})
	}
	if len(out) == 0 {
		return nil, scraper.ParseError(s.base+"/product_list.html", fmt.Errorf("no label links in the sidebar"))
	}
	scraper.Debugf(1, "%s: scraping the whole catalogue label by label (%d labels)", siteID, len(out))
	return out, nil
}

func (s *Scraper) walk(ctx context.Context, l listing, opts scraper.ListOpts) ([]item, error) {
	var items []item
	pages := 1
	for page := 1; page <= pages; page++ {
		if ctx.Err() != nil {
			return items, nil
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return items, nil
		}
		u := l.url(s.base, page)
		scraper.Debugf(1, "%s: fetching listing %s", siteID, u)
		body, err := s.get(ctx, u)
		if err != nil {
			return items, err
		}
		cards := parseCards(string(body))
		if page == 1 {
			if m := totalRe.FindStringSubmatch(string(body)); m != nil {
				n, _ := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
				pages = int(math.Ceil(float64(n) / pageSize))
			}
		}
		if len(cards) == 0 {
			break
		}
		items = append(items, cards...)
	}
	return items, nil
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

var (
	labelRe    = regexp.MustCompile(`product_list\.html\?label_id=(\d+)"`)
	totalRe    = regexp.MustCompile(`ヒット件数<STRONG>([\d,]+)</STRONG>`)
	cardRe     = regexp.MustCompile(`(?s)<A href="([A-Za-z0-9_-]+)\.html"><IMG src="(img/package\d+/[^"]+)" alt="([^"]*)".*?発売[^0-9]*(\d{4}/\d{1,2}/\d{1,2})`)
	titleRe    = regexp.MustCompile(`<title>(.*?)\s+-\s+グローバルメディア`)
	descRe     = regexp.MustCompile(`(?s)TABLE-layout:fixed;">\s*<STRONG>(.*?)</STRONG><BR>\s*(.*?)\s*</TD>`)
	dateRe     = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
	minutesRe  = regexp.MustCompile(`(\d+)分`)
	anchorRe   = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	bracketRe  = regexp.MustCompile(`\s*\[[^\]]*\]\s*$`)
	tagStripRe = regexp.MustCompile(`<[^>]*>`)
	brRe       = regexp.MustCompile(`(?i)<br\s*/?>`)
)

type item struct {
	code, title, thumb       string
	date                     time.Time
	performers               []string
	label, series, director  string
	description              string
	duration                 int
	detailDate               time.Time
	detailTitle, detailThumb string
}

func parseCards(page string) []item {
	var items []item
	for _, m := range cardRe.FindAllStringSubmatch(page, -1) {
		it := item{code: m[1], title: clean(m[3]), thumb: m[2]}
		if t, err := parseutil.TryParseDate(m[4], "2006/1/2"); err == nil {
			it.date = t.UTC()
		}
		if it.title != "" {
			items = append(items, it)
		}
	}
	return items
}

// row returns the value cell of a `<nobr>label</nobr>` row on a detail page.
func row(page, label string) string {
	re := regexp.MustCompile(`(?s)<nobr>` + regexp.QuoteMeta(label) + `</nobr></TD>\s*<TD[^>]*>.*?</TD>\s*<TD[^>]*>(.*?)</TD>`)
	if m := re.FindStringSubmatch(page); m != nil {
		return m[1]
	}
	return ""
}

func (it *item) applyDetail(page string) {
	if m := titleRe.FindStringSubmatch(page); m != nil {
		it.detailTitle = clean(m[1])
	}
	for _, a := range anchorRe.FindAllStringSubmatch(row(page, "出演者"), -1) {
		if n := clean(a[1]); n != "" {
			it.performers = append(it.performers, n)
		}
	}
	it.label = bracketRe.ReplaceAllString(clean(row(page, "レーベル")), "")
	it.series = clean(row(page, "シリーズ"))
	it.director = clean(row(page, "監督"))
	if m := minutesRe.FindStringSubmatch(row(page, "収録時間")); m != nil {
		n, _ := strconv.Atoi(m[1])
		it.duration = n * 60
	}
	if m := dateRe.FindStringSubmatch(row(page, "発売日")); m != nil {
		if t, err := parseutil.TryParseDate(m[1]+"-"+m[2]+"-"+m[3], "2006-1-2"); err == nil {
			it.detailDate = t.UTC()
		}
	}
	if m := descRe.FindStringSubmatch(page); m != nil {
		head, body := clean(m[1]), clean(brRe.ReplaceAllString(m[2], " "))
		it.description = strings.TrimSpace(head + "\n\n" + body)
	}
	if m := regexp.MustCompile(`<a href="(img/package\d+/` + regexp.QuoteMeta(it.code) + `\.jpg)" rel="lightbox">`).FindStringSubmatch(page); m != nil {
		it.detailThumb = m[1]
	}
}

func (it item) toScene(base, studioURL string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:          it.code,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Studio:      studioName,
		Title:       firstNonEmpty(it.detailTitle, it.title),
		URL:         base + "/" + it.code + ".html",
		Description: it.description,
		Thumbnail:   base + "/" + firstNonEmpty(it.detailThumb, it.thumb),
		Performers:  it.performers,
		Director:    it.director,
		Series:      it.series,
		Duration:    it.duration,
		Date:        it.date,
		ScrapedAt:   now,
	}
	if !it.detailDate.IsZero() {
		scene.Date = it.detailDate
	}
	if it.label != "" {
		scene.Studio = it.label
	}
	return scene
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}
