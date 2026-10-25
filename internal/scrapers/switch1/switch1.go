// Package switch1 scrapes switch1.jp, the Japanese AV maker SWITCH's own
// catalogue. See docs/scrapers.md.
package switch1

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

const (
	siteID        = "switch1"
	studioName    = "SWITCH"
	detailWorkers = 4
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?switch1\.jp(?:[/?#]|$)`)

	cardSplitRe = regexp.MustCompile(`<li class="gall_li`)
	cardIDRe    = regexp.MustCompile(`bo_table=works&amp;wr_id=(\d+)`)
	cardThumbRe = regexp.MustCompile(`<i class="imgAr"><img src="([^"]+)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<a href="[^"]*" class="bo_tit">(.*?)</a>`)
	cardMetaRe  = regexp.MustCompile(`(?s)<div class="gall_textMv">\s*<em>(.*?)</em>`)
	totalRe     = regexp.MustCompile(`Total\s+(\d+)`)
	pageLinkRe  = regexp.MustCompile(`[?&;]page=(\d+)" class="pg_page`)

	detailTitleRe = regexp.MustCompile(`(?s)<h2 id="bo_v_title">\s*<div>(.*?)</div>`)
	detailMetaRe  = regexp.MustCompile(`(?s)<div class="sbjB1">(.*?)</div>`)
	detailDescRe  = regexp.MustCompile(`(?s)<div class="sbjB1">.*?</div>\s*<ul>\s*<li>(.*?)</li>`)
	detailImgRe   = regexp.MustCompile(`(?s)<ul id='bigphoto'>\s*<li><a><img src="([^"]+)"`)

	minutesRe  = regexp.MustCompile(`(\d+)\s*分`)
	castSplit  = regexp.MustCompile(`[、,，/／]`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

// Scraper implements scraper.StudioScraper for switch1.jp.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the SWITCH scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: "https://switch1.jp"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"switch1.jp",
		"switch1.jp/web/",
		"switch1.jp/web/bbs/board.php?bo_table=works",
		"switch1.jp/web/bbs/board.php?bo_table=works&sca={year}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one WORKS listing entry. Its title is truncated with "…", so the
// detail page supplies the full title and the synopsis.
type card struct {
	wrID  string
	title string
	thumb string
	meta  string
}

// category returns the board's year category (sca=2026年) an operator URL
// selects, or "" for the whole catalogue.
func category(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("sca")
}

func (s *Scraper) listURL(sca string, page int) string {
	q := url.Values{"bo_table": {"works"}}
	if sca != "" {
		q.Set("sca", sca)
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	return s.base + "/web/bbs/board.php?" + q.Encode()
}

func (s *Scraper) detailURL(wrID string) string {
	return s.base + "/web/bbs/board.php?bo_table=works&wr_id=" + wrID
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	sca := category(studioURL)
	if sca != "" {
		scraper.Debugf(1, "%s: scraping category %q", siteID, sca)
	}
	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listURL(sca, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 && !strings.Contains(body, "Total 0") {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no works on the first page"))
		}

		scenes := make([]models.Scene, 0, len(cards))
		for _, c := range cards {
			sc := s.baseScene(c, studioURL, now)
			if seen[sc.ID] {
				continue
			}
			seen[sc.ID] = true
			scenes = append(scenes, sc)
		}
		var fresh []*models.Scene
		for i := range scenes {
			if !opts.KnownIDs[scenes[i].ID] {
				fresh = append(fresh, &scenes[i])
			}
		}
		s.enrich(ctx, fresh, opts, out)

		// A post added mid-walk shifts every page by one, so a page can repeat
		// only works already sent; that is not the end of the listing.
		res := scraper.PageResult{Scenes: scenes, Done: page >= lastPage(body), Continue: len(cards) > 0}
		if m := totalRe.FindStringSubmatch(body); m != nil {
			res.Total, _ = strconv.Atoi(m[1])
		}
		return res, nil
	})
}

// parseListing reads the WORKS gallery cards.
func parseListing(body string) []card {
	parts := cardSplitRe.Split(body, -1)
	var cards []card
	for _, blk := range parts[1:] {
		id := cardIDRe.FindStringSubmatch(blk)
		if id == nil {
			continue
		}
		c := card{wrID: id[1]}
		if m := cardTitleRe.FindStringSubmatch(blk); m != nil {
			c.title = cleanText(m[1])
		}
		if m := cardThumbRe.FindStringSubmatch(blk); m != nil {
			c.thumb = html.UnescapeString(m[1])
		}
		if m := cardMetaRe.FindStringSubmatch(blk); m != nil {
			c.meta = m[1]
		}
		cards = append(cards, c)
	}
	return cards
}

// lastPage is the highest page number the pagination bar links to.
func lastPage(body string) int {
	last := 1
	for _, m := range pageLinkRe.FindAllStringSubmatch(body, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > last {
			last = n
		}
	}
	return last
}

// meta is the hand-typed "○発売日：…　○収録時間：…分　○監督：…　○品番：…" line
// both the card and the detail page carry.
type meta struct {
	date       time.Time
	duration   int
	director   string
	code       string
	performers []string
}

func parseMeta(raw string) meta {
	var m meta
	for _, field := range strings.Split(cleanText(raw), "○") {
		label, val, ok := strings.Cut(field, "：")
		if !ok {
			continue
		}
		label, val = strings.TrimSpace(label), strings.TrimSpace(val)
		switch label {
		case "発売日":
			if d, err := parseutil.TryParseDate(val, "2006/1/2"); err == nil {
				m.date = d
			}
		case "収録時間":
			if mm := minutesRe.FindStringSubmatch(val); mm != nil {
				if n, err := strconv.Atoi(mm[1]); err == nil {
					m.duration = n * 60
				}
			}
		case "監督":
			m.director = val
		case "品番":
			m.code = strings.ToUpper(val)
		case "出演者":
			for _, n := range castSplit.Split(val, -1) {
				if n = strings.TrimSpace(n); n != "" && strings.Trim(n, "-－ー") != "" {
					m.performers = append(m.performers, n)
				}
			}
		}
	}
	return m
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	m := parseMeta(c.meta)
	id := m.code
	if id == "" {
		id = "wr" + c.wrID
	}
	sc := models.Scene{
		ID:        id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     strings.TrimSpace(strings.TrimSuffix(c.title, "…")),
		URL:       s.detailURL(c.wrID),
		Thumbnail: c.thumb,
		ScrapedAt: now,
	}
	applyMeta(&sc, m)
	return sc
}

func applyMeta(sc *models.Scene, m meta) {
	if !m.date.IsZero() {
		sc.Date = m.date
	}
	if m.duration > 0 {
		sc.Duration = m.duration
	}
	if m.director != "" {
		sc.Director = m.director
	}
	if len(m.performers) > 0 {
		sc.Performers = m.performers
	}
}

// enrich fetches each new scene's detail page for its full title, synopsis
// and full-size cover.
func (s *Scraper) enrich(ctx context.Context, scenes []*models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(scenes) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for _, sc := range scenes {
		wg.Add(1)
		go func(sc *models.Scene) {
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
			body, err := s.fetch(ctx, sc.URL)
			if err == nil {
				err = applyDetail(sc, body)
			}
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
			}
		}(sc)
	}
	wg.Wait()
}

// applyDetail folds a detail page into a scene built from its listing card.
func applyDetail(sc *models.Scene, body string) error {
	m := detailTitleRe.FindStringSubmatch(body)
	if m == nil {
		return scraper.ParseError(sc.URL, fmt.Errorf("no title block"))
	}
	if t := cleanText(m[1]); t != "" {
		sc.Title = t
	}
	if mm := detailMetaRe.FindStringSubmatch(body); mm != nil {
		applyMeta(sc, parseMeta(mm[1]))
	}
	if mm := detailDescRe.FindStringSubmatch(body); mm != nil {
		sc.Description = cleanText(mm[1])
	}
	if mm := detailImgRe.FindStringSubmatch(body); mm != nil {
		sc.Thumbnail = html.UnescapeString(mm[1])
	}
	return nil
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
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
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}
