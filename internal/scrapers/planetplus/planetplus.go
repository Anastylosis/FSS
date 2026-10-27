// Package planetplus scrapes Planet Plus (planetplus.jp), a Japanese AV
// maker whose labels (七狗留, アンビバレンツ, 裸婦趣向, エモーション, …) are
// categories of one WordPress install. See docs/scrapers.md.
package planetplus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "planetplus"
	studioName = "Planet Plus"
	perPage    = 50
)

var siteBase = "https://planetplus.jp/wp01"

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?planetplus\.jp(?:/|$)`)
	labelRe = regexp.MustCompile(`/category/label/([^/?#]+)`)
	rowRe   = regexp.MustCompile(`(?s)<tr>\s*<td[^>]*>([^<]+)</td>\s*<td[^>]*>\s*<div[^>]*>(.*?)</div>\s*</td>\s*</tr>`)
	dateRe  = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
	minRe   = regexp.MustCompile(`(\d+)\s*分`)
	yenRe   = regexp.MustCompile(`([\d,]+)\s*円`)
	stripRe = regexp.MustCompile(`<[^>]+>`)
	splitRe = regexp.MustCompile(`[、,／/]\s*`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"planetplus.jp", "planetplus.jp/wp01/category/label/{slug}/"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type post struct {
	ID      int    `json:"id"`
	DateGMT string `json:"date_gmt"`
	Link    string `json:"link"`
	Title   struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Content struct {
		Rendered string `json:"rendered"`
	} `json:"content"`
	Embedded struct {
		Media []struct {
			SourceURL string `json:"source_url"`
		} `json:"wp:featuredmedia"`
	} `json:"_embedded"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	filter := ""
	if m := labelRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping label %s", siteID, m[1])
		id, err := s.categoryID(ctx, m[1])
		if err != nil {
			select {
			case out <- scraper.Error(err):
			case <-ctx.Done():
			}
			return
		}
		filter = "&categories=" + strconv.Itoa(id)
	}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/wp-json/wp/v2/posts?per_page=%d&page=%d&orderby=date&order=desc&_embed=wp:featuredmedia%s",
			siteBase, perPage, page, filter)
		var posts []post
		total, pages, err := s.getJSON(ctx, u, &posts)
		if err != nil {
			var se *httpx.StatusError
			// WordPress answers 400 for a page past the last.
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusBadRequest {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, 0, len(posts))
		for _, p := range posts {
			scenes = append(scenes, toScene(p, studioURL, now))
		}
		res := scraper.PageResult{Scenes: scenes, Done: len(posts) < perPage || (pages > 0 && page >= pages)}
		if page == 1 {
			res.Total = total
		}
		return res, nil
	})
}

func (s *Scraper) categoryID(ctx context.Context, slug string) (int, error) {
	u := siteBase + "/wp-json/wp/v2/categories?slug=" + url.QueryEscape(slug) + "&_fields=id"
	var cats []struct {
		ID int `json:"id"`
	}
	if _, _, err := s.getJSON(ctx, u, &cats); err != nil {
		return 0, err
	}
	if len(cats) == 0 {
		return 0, scraper.ParseError(u, fmt.Errorf("no label %q", slug))
	}
	return cats[0].ID, nil
}

func (s *Scraper) getJSON(ctx context.Context, u string, v any) (total, pages int, err error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, "https://planetplus.jp")})
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	total, _ = strconv.Atoi(resp.Header.Get("X-WP-Total"))
	pages, _ = strconv.Atoi(resp.Header.Get("X-WP-TotalPages"))
	if err := httpx.DecodeJSON(resp.Body, v); err != nil {
		var se *json.SyntaxError
		if errors.As(err, &se) {
			return 0, 0, scraper.ParseError(u, err)
		}
		return 0, 0, err
	}
	return total, pages, nil
}

// toScene reads the post's spec table: 品番 (code), 企画・制作 (label), 定価,
// 発売日, 収録時間, 出演女優, 監督 and ストーリー.
func toScene(p post, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:        strconv.Itoa(p.ID),
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     clean(p.Title.Rendered),
		URL:       p.Link,
		Studio:    studioName,
		ScrapedAt: now,
	}
	if t, err := time.Parse("2006-01-02T15:04:05", p.DateGMT); err == nil {
		sc.Date = t.UTC()
	}
	if len(p.Embedded.Media) > 0 {
		sc.Thumbnail = p.Embedded.Media[0].SourceURL
	}
	for _, m := range rowRe.FindAllStringSubmatch(p.Content.Rendered, -1) {
		label, value := strings.TrimSpace(m[1]), m[2]
		v := clean(value)
		switch label {
		case "品番":
			if v != "" {
				sc.ID = v
			}
		case "企画・制作":
			sc.Series = v
		case "発売日":
			if d := dateRe.FindStringSubmatch(v); d != nil {
				y, _ := strconv.Atoi(d[1])
				mo, _ := strconv.Atoi(d[2])
				da, _ := strconv.Atoi(d[3])
				sc.Date = time.Date(y, time.Month(mo), da, 0, 0, 0, 0, time.UTC)
			}
		case "収録時間":
			if d := minRe.FindStringSubmatch(v); d != nil {
				n, _ := strconv.Atoi(d[1])
				sc.Duration = n * 60
			}
		case "出演女優":
			for _, name := range splitRe.Split(v, -1) {
				if name = strings.TrimSpace(name); name != "" && name != "-" {
					sc.Performers = append(sc.Performers, name)
				}
			}
		case "監督":
			sc.Director = v
		case "ストーリー":
			sc.Description = clean(strings.ReplaceAll(value, "<br />", "\n"))
		case "定価":
			if d := yenRe.FindStringSubmatch(v); d != nil {
				if n, err := strconv.Atoi(strings.ReplaceAll(d[1], ",", "")); err == nil && n > 0 {
					sc.AddPrice(models.PriceSnapshot{Date: now, Regular: float64(n)})
				}
			}
		}
	}
	return sc
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, " "))), " ")
}
