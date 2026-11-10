// Package hibino scrapes av-hibino.com, a gnuboard "works" board. The list card
// already carries date, duration, cast, director and product code as one line
// of ○-separated fields; the detail page adds the synopsis and full-size cover.
package hibino

import (
	"context"
	"fmt"
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

const (
	siteID     = "hibino"
	studioName = "Hibino"
	boardPath  = "/web/bbs/board.php?bo_table=works"
)

var siteBase = "https://av-hibino.com"

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?av-hibino\.com(?:/|$)`)

type Scraper struct {
	client *http.Client
}

func New() *Scraper { return &Scraper{client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"av-hibino.com",
		"av-hibino.com/web/bbs/board.php?bo_table=works",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.Debugf(1, "%s: walking the works board", siteID)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s&page=%d", siteBase, boardPath, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := parseList(string(body))
		if len(items) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no works on the first page"))
			}
			return scraper.PageResult{Done: true}, nil
		}
		now := time.Now().UTC()
		scenes := make([]models.Scene, 0, len(items))
		for _, it := range items {
			sc := it.scene(studioURL, now)
			if !opts.KnownIDs[sc.ID] {
				s.addDetail(ctx, &sc, it.wrID, opts, out)
			}
			scenes = append(scenes, sc)
		}
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// addDetail fills the synopsis and cover from the detail page. A failure is
// reported but does not drop the scene, which the list card already describes.
func (s *Scraper) addDetail(ctx context.Context, sc *models.Scene, wrID string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if !scraper.Pace(ctx, opts.Delay) {
		return
	}
	body, err := s.fetch(ctx, sc.URL)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("detail %s: %w", wrID, err)):
		case <-ctx.Done():
		}
		return
	}
	desc, cover := parseDetail(string(body))
	sc.Description = desc
	if cover != "" {
		sc.Thumbnail = cover
	}
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		Method:  http.MethodGet,
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

type item struct {
	wrID, title, thumb string
	fields             map[string]string
}

var (
	liRe     = regexp.MustCompile(`(?s)<li class="gall_li.*?</li>`)
	wrIDRe   = regexp.MustCompile(`wr_id=(\d+)`)
	titleRe  = regexp.MustCompile(`(?s)<a [^>]*class="bo_tit"[^>]*>(.*?)</a>`)
	thumbRe  = regexp.MustCompile(`<i class="imgAr"><img src="([^"]+)"`)
	infoRe   = regexp.MustCompile(`(?s)<div class="gall_textMv">\s*<em>(.*?)</em>`)
	detailDe = regexp.MustCompile(`(?s)class="sbjB1">.*?</div>\s*<ul>\s*<li>(.*?)</li>`)
	coverRe  = regexp.MustCompile(`(?s)<ul id='bigphoto'>\s*<li><a><img src="([^"]+)"`)
	tagRe    = regexp.MustCompile(`<[^>]*>`)
	dateRe   = regexp.MustCompile(`(\d{4})/0*(\d{1,2})/0*(\d{1,2})`)
	minRe    = regexp.MustCompile(`(\d+)\s*分`)
	splitRe  = regexp.MustCompile(`[、,，/／]`)
)

func parseList(body string) []item {
	var items []item
	seen := map[string]bool{}
	for _, li := range liRe.FindAllString(body, -1) {
		m := wrIDRe.FindStringSubmatch(li)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		it := item{wrID: m[1], fields: map[string]string{}}
		if t := titleRe.FindStringSubmatch(li); t != nil {
			it.title = clean(t[1])
		}
		if t := thumbRe.FindStringSubmatch(li); t != nil {
			it.thumb = t[1]
		}
		if t := infoRe.FindStringSubmatch(li); t != nil {
			for _, part := range strings.Split(clean(t[1]), "○") {
				if k, v, ok := strings.Cut(part, "："); ok {
					it.fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
		}
		items = append(items, it)
	}
	return items
}

func (it item) scene(studioURL string, now time.Time) models.Scene {
	id := it.fields["品番"]
	if id == "" {
		id = "wr" + it.wrID
	}
	sc := models.Scene{
		ID:        id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       fmt.Sprintf("%s%s&wr_id=%s", siteBase, boardPath, it.wrID),
		Thumbnail: it.thumb,
		Director:  it.fields["監督"],
		Studio:    studioName,
		ScrapedAt: now,
	}
	if d := dateRe.FindStringSubmatch(it.fields["発売日"]); d != nil {
		y, _ := strconv.Atoi(d[1])
		mo, _ := strconv.Atoi(d[2])
		dd, _ := strconv.Atoi(d[3])
		sc.Date = time.Date(y, time.Month(mo), dd, 0, 0, 0, 0, time.UTC)
	}
	if d := minRe.FindStringSubmatch(it.fields["収録時間"]); d != nil {
		n, _ := strconv.Atoi(d[1])
		sc.Duration = n * 60
	}
	for _, p := range splitRe.Split(it.fields["出演者"], -1) {
		if p = strings.TrimSpace(p); p != "" {
			sc.Performers = append(sc.Performers, p)
		}
	}
	return sc
}

func parseDetail(body string) (desc, cover string) {
	if m := detailDe.FindStringSubmatch(body); m != nil {
		desc = clean(m[1])
	}
	if m := coverRe.FindStringSubmatch(body); m != nil {
		cover = m[1]
	}
	return desc, cover
}

func clean(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "　", " ")
	return strings.Join(strings.Fields(s), " ")
}
