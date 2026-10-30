// Package sadisticvillage scrapes Sadistic Village (sadisticvill.com), a
// Japanese AV maker publishing each release as a WordPress post. See
// docs/scrapers.md.
package sadisticvillage

import (
	"context"
	"encoding/json"
	"errors"
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
	siteID     = "sadisticvillage"
	studioName = "Sadistic Village"
	perPage    = 50
	worksCat   = 7
)

var siteBase = "https://sadisticvill.com"

var (
	matchRe   = regexp.MustCompile(`^https?://(?:www\.)?sadisticvill\.com(?:/|$)`)
	sectionRe = regexp.MustCompile(`<h1>([^<]*)</h1>`)
	buttonRe  = regexp.MustCompile(`(?s)<a class="wp-block-button__link[^"]*"[^>]*>(.*?)</a>`)
	paraRe    = regexp.MustCompile(`(?s)<p[^>]*>(.*?)</p>`)
	dateRe    = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
	codeRe    = regexp.MustCompile(`([A-Z]{2,}-\d+)\s*・\s*(\d+)\s*分`)
	imgRe     = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	stripRe   = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string { return []string{"sadisticvill.com"} }

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
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/wp-json/wp/v2/posts?categories=%d&per_page=%d&page=%d&orderby=date&order=desc&_fields=id,date_gmt,link,title,content",
			siteBase, worksCat, perPage, page)
		var posts []post
		total, pages, err := s.getJSON(ctx, u, &posts)
		if err != nil {
			var se *httpx.StatusError
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

func (s *Scraper) getJSON(ctx context.Context, u string, v any) (total, pages int, err error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, siteBase)})
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

func sections(body string) map[string]string {
	out := map[string]string{}
	locs := sectionRe.FindAllStringSubmatchIndex(body, -1)
	for i, l := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out[strings.TrimSpace(body[l[2]:l[3]])] = body[l[1]:end]
	}
	return out
}

func buttons(s string) []string {
	var out []string
	for _, m := range buttonRe.FindAllStringSubmatch(s, -1) {
		if v := clean(m[1]); v != "" {
			out = append(out, v)
		}
	}
	return out
}

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
	body := commentRe.ReplaceAllString(p.Content.Rendered, "")
	sec := sections(body)

	if m := paraRe.FindStringSubmatch(sec["作品情報"]); m != nil {
		sc.Description = clean(m[1])
	}
	sc.Performers = buttons(sec["出演"])
	sc.Tags = buttons(sec["ジャンル"])
	if v := buttons(sec["シリーズ名"]); len(v) > 0 {
		sc.Series = v[0]
	}
	if v := buttons(sec["監督"]); len(v) > 0 {
		sc.Director = v[0]
	}
	var earliest time.Time
	for _, d := range dateRe.FindAllStringSubmatch(clean(sec["発売日"]), -1) {
		y, _ := strconv.Atoi(d[1])
		mo, _ := strconv.Atoi(d[2])
		da, _ := strconv.Atoi(d[3])
		t := time.Date(y, time.Month(mo), da, 0, 0, 0, 0, time.UTC)
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	if !earliest.IsZero() {
		sc.Date = earliest
	}
	if m := codeRe.FindStringSubmatch(clean(sec["品番・収録時間"])); m != nil {
		sc.ID = m[1]
		n, _ := strconv.Atoi(m[2])
		sc.Duration = n * 60
		sc.Thumbnail = codeImage(body, m[1])
	}
	return sc
}

// codeImage returns the first post image named after the product code; the
// site reuses one featured image and copies sample stills between posts.
func codeImage(body, code string) string {
	want := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	for _, m := range imgRe.FindAllStringSubmatch(body, -1) {
		src := html.UnescapeString(m[1])
		if strings.Contains(strings.ToLower(strings.ReplaceAll(src, "-", "")), want) {
			return src
		}
	}
	return ""
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, ""))), " ")
}
