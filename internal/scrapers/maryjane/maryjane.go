// Package maryjane scrapes mary-jane.biz, a hand-written static catalogue.
// HTTPS serves a mismatched certificate, so the plain-http origin is the base.
package maryjane

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
	siteID     = "maryjane"
	studioName = "Mary Jane"
	listPath   = "/mysite1/shouhinlist.html"
)

var siteBase = "http://mary-jane.biz"

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?mary-jane\.biz(?:/|$)`)

type Scraper struct {
	client *http.Client
}

func New() *Scraper { return &Scraper{client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"mary-jane.biz", "mary-jane.biz/mysite1/shouhinlist.html"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.Debugf(1, "%s: reading the single-page catalogue", siteID)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, _ int) (scraper.PageResult, error) {
		listURL := siteBase + listPath
		resp, err := httpx.Do(ctx, s.client, httpx.Request{
			Method:  http.MethodGet,
			URL:     listURL,
			Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
		})
		if err != nil {
			return scraper.PageResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := httpx.ReadBody(resp.Body)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parseList(string(body), studioURL, listURL, time.Now().UTC())
		if len(scenes) == 0 {
			return scraper.PageResult{}, scraper.ParseError(listURL, fmt.Errorf("no products found"))
		}
		return scraper.PageResult{Scenes: scenes, Total: len(scenes), Done: true}, nil
	})
}

var (
	rowRe   = regexp.MustCompile(`(?is)<TR[^>]*>(.*?)</TR>`)
	cellRe  = regexp.MustCompile(`(?is)<TD[^>]*>(.*?)</TD>`)
	imgRe   = regexp.MustCompile(`(?i)<IMG[^>]+src="([^"]+)"`)
	brRe    = regexp.MustCompile(`(?i)<BR\s*/?>`)
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	codeRe  = regexp.MustCompile(`型番[：:]\s*([A-Za-z0-9\-]+)`)
	dateRe  = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
	priceRe = regexp.MustCompile(`価格[：:]\s*[￥¥]\s*([\d,]+)`)
)

// parseList pairs each caption cell with the picture in the same column of the
// row above it: the page is a grid of picture rows and caption rows.
func parseList(body, studioURL, listURL string, now time.Time) []models.Scene {
	var scenes []models.Scene
	seen := map[string]bool{}
	var prevImgs []string
	for _, r := range rowRe.FindAllStringSubmatch(body, -1) {
		cells := cellRe.FindAllStringSubmatch(r[1], -1)
		imgs := make([]string, len(cells))
		hasText := false
		for i, c := range cells {
			if m := imgRe.FindStringSubmatch(c[1]); m != nil {
				imgs[i] = m[1]
			}
			if codeRe.MatchString(c[1]) {
				hasText = true
			}
		}
		if !hasText {
			prevImgs = imgs
			continue
		}
		for i, c := range cells {
			sc, ok := parseCell(c[1], studioURL, listURL, now)
			if !ok || seen[sc.ID] {
				continue
			}
			seen[sc.ID] = true
			if i < len(prevImgs) && prevImgs[i] != "" {
				sc.Thumbnail = siteBase + "/mysite1/" + strings.TrimPrefix(prevImgs[i], "/")
			}
			scenes = append(scenes, sc)
		}
	}
	return scenes
}

func parseCell(cell, studioURL, listURL string, now time.Time) (models.Scene, bool) {
	m := codeRe.FindStringSubmatch(cell)
	if m == nil {
		return models.Scene{}, false
	}
	code := m[1]
	var title []string
	var date time.Time
	var price float64
	for _, line := range brRe.Split(cell, -1) {
		line = clean(line)
		switch {
		case line == "":
		case dateRe.MatchString(line):
			d := dateRe.FindStringSubmatch(line)
			y, _ := strconv.Atoi(d[1])
			mo, _ := strconv.Atoi(d[2])
			dd, _ := strconv.Atoi(d[3])
			date = time.Date(y, time.Month(mo), dd, 0, 0, 0, 0, time.UTC)
		case priceRe.MatchString(line):
			p := priceRe.FindStringSubmatch(line)
			price, _ = strconv.ParseFloat(strings.ReplaceAll(p[1], ",", ""), 64)
		case strings.Contains(line, "型番"), strings.Contains(line, "ＪＡＮ"), strings.Contains(line, "JAN"):
		default:
			title = append(title, line)
		}
	}
	if len(title) == 0 {
		return models.Scene{}, false
	}
	sc := models.Scene{
		ID:        code,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     strings.Join(title, " "),
		URL:       listURL + "#" + code,
		Date:      date,
		Studio:    studioName,
		ScrapedAt: now,
	}
	if price > 0 {
		sc.AddPrice(models.PriceSnapshot{Date: now, Regular: price})
	}
	return sc, true
}

func clean(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	s = strings.ReplaceAll(s, "　", " ")
	return strings.Join(strings.Fields(s), " ")
}
