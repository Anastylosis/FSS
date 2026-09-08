// Package maxing scrapes maxing.jp.
package maxing

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

	"golang.org/x/text/encoding/japanese"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "maxing"
	studioName = "Maxing"
	defaultURL = "https://www.maxing.jp"
	// maxPages backstops the walk; the real stop is a page with no products.
	maxPages       = 300
	defaultWorkers = 6
)

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(45 * time.Second), base: defaultURL}
}

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"maxing.jp",
		"maxing.jp/shop/",
		"maxing.jp/shop/ac/ACT{id}.html",
		"maxing.jp/shop/sr/{id}.html",
		"maxing.jp/shop/la/{id}.html",
	}
}

var urlRe = regexp.MustCompile(`^https?://(?:www\.)?maxing\.jp(?:/.*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return urlRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// get fetches and decodes one page. **The site is EUC-JP**, not UTF-8, and
// declares it only in a meta tag; reading the bytes as UTF-8 turns every
// Japanese title, name and label into replacement characters.
func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	decoded, err := japanese.EUCJP.NewDecoder().Bytes(raw)
	if err != nil {
		// A page that is not valid EUC-JP is still worth parsing — the URLs
		// and numbers in it are ASCII either way.
		return raw, nil
	}
	return decoded, nil
}

// ---- listing ----

var (
	productRe    = regexp.MustCompile(`/shop/pid/([^"./]+)\.html`)
	filterPathRe = regexp.MustCompile(`(?i)^/shop/(?:ac|sr|la|mk)/[^/]+\.html$`)
)

func (s *Scraper) listingURL(filter string, page int) string {
	if filter != "" {
		return s.base + filter
	}
	return fmt.Sprintf("%s/shop/src/page/%d.html", s.base, page)
}

// filterPath returns the single page to read for an actress, series, label or
// maker URL, or "" for the paged catalogue.
func filterPath(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return ""
	}
	if filterPathRe.MatchString(u.Path) {
		return u.Path
	}
	return ""
}

func productIDs(body []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range productRe.FindAllSubmatch(body, -1) {
		id := string(m[1])
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// ---- run ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	filter := filterPath(studioURL)
	if filter != "" {
		scraper.Debugf(1, "%s: scraping %s", siteID, filter)
	} else {
		scraper.Debugf(1, "%s: scraping the full catalogue", siteID)
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if page > maxPages {
			scraper.Debugf(1, "%s: stopped at the %d-page cap", siteID, maxPages)
			return scraper.PageResult{Done: true}, nil
		}
		u := s.listingURL(filter, page)
		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}

		var fresh []string
		for _, id := range productIDs(body) {
			if seen[id] {
				continue
			}
			seen[id] = true
			fresh = append(fresh, id)
		}
		if len(fresh) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("no products on the first page"))
			}
			return scraper.PageResult{Done: true}, nil
		}

		scenes := s.fetchDetails(ctx, fresh, studioURL, workers, out)
		return scraper.PageResult{
			Scenes: scenes,
			Done:   filter != "",
		}, nil
	})
}

func (s *Scraper) fetchDetails(ctx context.Context, ids []string, studioURL string, workers int, out chan<- scraper.SceneResult) []models.Scene {
	results := make([]*models.Scene, len(ids))
	errs := make([]error, len(ids))

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, len(ids)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				u := fmt.Sprintf("%s/shop/pid/%s.html", s.base, ids[i])
				body, err := s.get(ctx, u)
				if err != nil {
					errs[i] = err
					continue
				}
				sc, err := s.parseProduct(body, ids[i], u, studioURL)
				if err != nil {
					errs[i] = err
					continue
				}
				results[i] = sc
			}
		}()
	}
	func() {
		defer close(jobs)
		for i := range ids {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()

	var scenes []models.Scene
	for i, sc := range results {
		if errs[i] != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("%s: product %s: %w", siteID, ids[i], errs[i])):
			case <-ctx.Done():
				return scenes
			}
			continue
		}
		if sc != nil {
			scenes = append(scenes, *sc)
		}
	}
	return scenes
}

// ---- parsing ----

var (
	detailListRe = regexp.MustCompile(`(?is)<dl class="pDetailDl">(.*?)</dl>`)
	pairRe       = regexp.MustCompile(`(?is)<dt[^>]*>(.*?)</dt>\s*<dd[^>]*>(.*?)</dd>`)
	titleTagRe   = regexp.MustCompile(`(?is)<title>(.*?)</title>`)
	// The cover is linked twice: once through a popup wrapper and once as a
	// direct <img src>. Only the src is a usable image URL.
	coverRe = regexp.MustCompile(`(?i)<img[^>]+src="(https?://[^"]*/product_img/[^"]+\.jpg)"`)
	// 2025年12月16日
	jpDateRe   = regexp.MustCompile(`(\d{4})年\s*(\d{1,2})月\s*(\d{1,2})日`)
	tagStripRe = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe       = regexp.MustCompile(`\s+`)
)

// Japanese field labels on the product page. They are matched exactly rather
// than by position: the table omits a row entirely when a release has no
// director or series.
const (
	labelCode     = "品番"
	labelDirector = "監督"
	labelRuntime  = "収録時間"
	labelReleased = "発売日"
	labelLabel    = "レーベル"
	labelSeries   = "シリーズ"
	labelActress  = "女優"
	labelGenre    = "ジャンル"
	labelSummary  = "内容"
)

func (s *Scraper) parseProduct(body []byte, pid, productURL, studioURL string) (*models.Scene, error) {
	block := detailListRe.FindSubmatch(body)
	if block == nil {
		return nil, scraper.ParseError(productURL, fmt.Errorf("no pDetailDl block"))
	}
	fields := map[string]string{}
	links := map[string][]string{}
	for _, m := range pairRe.FindAllSubmatch(block[1], -1) {
		label := cleanText(string(m[1]))
		fields[label] = cleanText(string(m[2]))
		links[label] = anchorTexts(m[2])
	}

	sc := &models.Scene{
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		URL:       productURL,
		ScrapedAt: time.Now().UTC(),
	}

	// The catalogue number is how a release is identified everywhere else,
	// including fss's own javdatabase scraper, so it is the id where present;
	// the shop's internal pid is only the fallback.
	sc.ID = fields[labelCode]
	if sc.ID == "" {
		sc.ID = pid
	}

	if m := titleTagRe.FindSubmatch(body); m != nil {
		sc.Title = trimSiteSuffix(cleanText(string(m[1])))
	}
	if sc.Title == "" {
		return nil, scraper.ParseError(productURL, fmt.Errorf("no title"))
	}

	sc.Description = fields[labelSummary]
	sc.Director = fields[labelDirector]
	// 収録時間 is stated in whole minutes.
	if mins, err := strconv.Atoi(strings.TrimSpace(fields[labelRuntime])); err == nil && mins > 0 {
		sc.Duration = mins * 60
	}
	if m := jpDateRe.FindStringSubmatch(fields[labelReleased]); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		sc.Date = time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	}
	if v := links[labelSeries]; len(v) > 0 {
		sc.Series = v[0]
	}
	sc.Performers = links[labelActress]
	sc.Categories = links[labelGenre]
	// The label is the studio itself on most releases; only a sub-label is
	// worth recording.
	if v := links[labelLabel]; len(v) > 0 && !strings.EqualFold(v[0], studioName) {
		sc.Categories = append(sc.Categories, v[0])
	}
	if m := coverRe.FindSubmatch(body); m != nil {
		sc.Thumbnail = html.UnescapeString(string(m[1]))
	}
	return sc, nil
}

func anchorTexts(seg []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range anchorRe.FindAllSubmatch(seg, -1) {
		v := cleanText(string(m[1]))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

var anchorRe = regexp.MustCompile(`(?is)<a[^>]*>(.*?)</a>`)

// trimSiteSuffix drops the "★マキシング" the site appends to every <title>.
func trimSiteSuffix(s string) string {
	if i := strings.LastIndex(s, "★"); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(html.UnescapeString(s))
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}
