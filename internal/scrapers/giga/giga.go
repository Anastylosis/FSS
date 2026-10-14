// Package giga scrapes giga-web.jp, the GIGA tokusatsu-heroine label's own
// catalogue. See docs/scrapers.md.
package giga

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
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
	siteID     = "giga"
	studioName = "GIGA"
	// detailWorkers caps the per-page detail pool; the listing is 20 cards a
	// page over ~220 pages and cast, runtime and story live on the detail page.
	detailWorkers = 4
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?giga-web\.jp(?:/|$)`)

	cardSplitRe = regexp.MustCompile(`class="col s6 m4 l3 center thumBox"`)
	cardIDRe    = regexp.MustCompile(`product_id=(\d+)`)
	cardTitleRe = regexp.MustCompile(`(?s)<span style="font-weight:bold;">(.*?)</span>`)
	cardCodeRe  = regexp.MustCompile(`</a>\s*（([^）<]+)）`)
	cardThumbRe = regexp.MustCompile(`<img src="([^"]+)"`)
	cardDateRe  = regexp.MustCompile(`リリース日[\s　]*(\d{4}-\d{2}-\d{2})`)

	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	tagStrip  = regexp.MustCompile(`<[^>]+>`)

	detailTitleRe = regexp.MustCompile(`(?s)<h5[^>]*>(.*?)</h5>`)
	detailThumbRe = regexp.MustCompile(`<img src="([^"]+/pac_s\.jpg)"`)
	storyRe       = regexp.MustCompile(`(?s)<dt>ストーリー</dt>.*?<li class="story_window">(.*?)<span>`)
	fieldRe       = regexp.MustCompile(`(?s)<li><dl><dt>([^<]+)</dt><dd>(.*?)</dd></dl></li>`)
	actorRe       = regexp.MustCompile(`(?s)actor_id=\d+"[^>]*>(.*?)</a>`)
	tagItemRe     = regexp.MustCompile(`(?s)<a href="\.\./search/index\.php\?tag_id=\d+"[^>]*>(.*?)</a>`)
	runtimeRe     = regexp.MustCompile(`本編(\d+)分`)
	anyMinutesRe  = regexp.MustCompile(`(\d+)分`)
	releaseDateRe = regexp.MustCompile(`(\d{4})/(\d{2})/(\d{2})`)

	// Filter parameters the listing honours; everything else (sort, narrow,
	// cp, paging) is rebuilt per request.
	filterKeys = []string{"actor_id", "series_id", "label_id", "tag_id", "year", "month", "keyword"}
)

// Scraper implements scraper.StudioScraper for giga-web.jp.
type Scraper struct {
	client *http.Client
	base   string

	mu      sync.Mutex
	session bool
}

// New builds the GIGA scraper.
func New() *Scraper {
	jar, _ := cookiejar.New(nil)
	c := httpx.NewClient(45 * time.Second)
	c.Jar = jar
	return &Scraper{client: c, base: "https://www.giga-web.jp"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"giga-web.jp",
		"giga-web.jp/search/index.php",
		"giga-web.jp/search/index.php?actor_id={id}",
		"giga-web.jp/search/index.php?label_id={id}",
		"giga-web.jp/search/index.php?series_id={id}",
		"giga-web.jp/search/index.php?tag_id={id}",
		"giga-web.jp/search/index.php?year={year}&month={month}",
		"giga-web.jp/search/index.php?keyword={text}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one listing entry.
type card struct {
	id    string
	title string
	code  string
	thumb string
	date  time.Time
}

// filterQuery keeps the listing filters of an operator URL; anything else
// walks the whole catalogue.
func filterQuery(studioURL string) url.Values {
	q := url.Values{}
	u, err := url.Parse(studioURL)
	if err != nil {
		return q
	}
	in := u.Query()
	for _, k := range filterKeys {
		if v := in.Get(k); v != "" {
			q.Set(k, v)
		}
	}
	return q
}

func (s *Scraper) listingURL(filter url.Values, page int) string {
	q := url.Values{}
	for k, v := range filter {
		q[k] = v
	}
	if page > 1 {
		q.Set("count", strconv.Itoa(page))
	}
	u := s.base + "/search/index.php"
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	return u
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	filter := filterQuery(studioURL)
	scraper.Debugf(1, "%s: listing filter %v", siteID, filter)

	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(filter, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 && !strings.Contains(body, "thumBox") {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}

		fresh := cards[:0]
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			fresh = append(fresh, c)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		scenes := make([]models.Scene, len(fresh))
		var toEnrich []int
		for i, c := range fresh {
			scenes[i] = s.baseScene(c, studioURL, now)
			if !opts.KnownIDs[scenes[i].ID] && !opts.KnownIDs[c.id] {
				toEnrich = append(toEnrich, i)
			}
		}
		s.enrich(ctx, scenes, toEnrich, opts, out)
		for i := range scenes {
			if scenes[i].ID == "" {
				scenes[i].ID = fresh[i].id
			}
		}
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// parseListing reads the listing's scene cards. Performers and director sit
// in HTML comments there, so comments are dropped first.
func parseListing(body string) []card {
	body = commentRe.ReplaceAllString(body, "")
	idx := cardSplitRe.FindAllStringIndex(body, -1)
	cards := make([]card, 0, len(idx))
	for i, m := range idx {
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		block := body[m[0]:end]

		idm := cardIDRe.FindStringSubmatch(block)
		tm := cardTitleRe.FindStringSubmatch(block)
		if idm == nil || tm == nil {
			continue
		}
		c := card{id: idm[1], title: cleanText(tm[1])}
		if cm := cardCodeRe.FindStringSubmatch(block); cm != nil {
			c.code = strings.TrimSpace(html.UnescapeString(cm[1]))
		}
		if th := cardThumbRe.FindStringSubmatch(block); th != nil {
			c.thumb = html.UnescapeString(th[1])
		}
		if dm := cardDateRe.FindStringSubmatch(block); dm != nil {
			if d, err := parseutil.TryParseDate(dm[1], "2006-01-02"); err == nil {
				c.date = d
			}
		}
		cards = append(cards, c)
	}
	return cards
}

// baseScene keys the scene on the catalogue number, as the javdatabase and
// maxing scrapers do; the site's product_id is only the fallback.
func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:        c.code,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     c.title,
		URL:       s.base + "/product/index.php?product_id=" + c.id,
		Date:      c.date,
		Thumbnail: c.thumb,
		ScrapedAt: now,
	}
}

func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, idx []int, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(idx) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(idx), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for _, i := range idx {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(&scenes[i], body)
		}()
	}
	wg.Wait()
}

// applyDetail folds the detail page into a scene built from its listing card.
func applyDetail(sc *models.Scene, body string) {
	body = commentRe.ReplaceAllString(body, "")

	if sc.Title == "" {
		if t := detailTitleRe.FindStringSubmatch(body); t != nil {
			sc.Title = cleanText(t[1])
		}
	}
	if sc.Thumbnail == "" {
		if th := detailThumbRe.FindStringSubmatch(body); th != nil {
			sc.Thumbnail = html.UnescapeString(th[1])
		}
	}
	if st := storyRe.FindStringSubmatch(body); st != nil {
		sc.Description = cleanText(st[1])
	}

	for _, f := range fieldRe.FindAllStringSubmatch(body, -1) {
		val := f[2]
		switch strings.TrimSpace(f[1]) {
		case "作品番号":
			if sc.ID == "" {
				sc.ID = cleanText(val)
			}
		case "出演女優":
			for _, a := range actorRe.FindAllStringSubmatch(val, -1) {
				if name := cleanText(a[1]); name != "" {
					sc.Performers = append(sc.Performers, name)
				}
			}
		case "監督":
			sc.Director = cleanText(val)
		case "収録時間":
			m := runtimeRe.FindStringSubmatch(val)
			if m == nil {
				m = anyMinutesRe.FindStringSubmatch(val)
			}
			if m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil {
					sc.Duration = n * 60
				}
			}
		case "DVDリリース日":
			if sc.Date.IsZero() {
				if m := releaseDateRe.FindStringSubmatch(val); m != nil {
					if d, err := parseutil.TryParseDate(m[1]+"-"+m[2]+"-"+m[3], "2006-01-02"); err == nil {
						sc.Date = d
					}
				}
			}
		}
	}

	if i := strings.Index(body, `id="tag_main"`); i >= 0 {
		section := body[i:]
		if j := strings.Index(section, `id="tag"`); j >= 0 {
			section = section[:j]
		}
		for _, t := range tagItemRe.FindAllStringSubmatch(section, -1) {
			if tag := cleanText(t[1]); tag != "" {
				sc.Tags = append(sc.Tags, tag)
			}
		}
	}
}

// ensureSession performs the age-gate handshake once; the gate is a cookie
// set by cookie_set.php and confirmed by loading top.php.
func (s *Scraper) ensureSession(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session {
		return nil
	}
	for _, p := range []string{"/cookie_set.php", "/top.php"} {
		if _, err := s.get(ctx, s.base+p); err != nil {
			return err
		}
	}
	s.session = true
	return nil
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	if err := s.ensureSession(ctx); err != nil {
		return "", err
	}
	return s.get(ctx, pageURL)
}

// get sends the Referer the site insists on: without it every page redirects
// back to the gate.
func (s *Scraper) get(ctx context.Context, pageURL string) (string, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	headers["Referer"] = s.base + "/top.php"
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: pageURL, Headers: headers})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStrip.ReplaceAllString(s, " "))), " ")
}
