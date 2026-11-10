// Package milu scrapes MILU (milu-av.com), a Japanese AV maker whose site is
// a WordPress install with pretty permalinks and wp-json disabled, so the
// REST API is reached through ?rest_route=. See docs/scrapers.md.
package milu

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
	siteID     = "milu"
	studioName = "MILU"
	perPage    = 30
)

var siteBase = "http://www.milu-av.com"

var (
	matchRe   = regexp.MustCompile(`^https?://(?:www\.)?milu-av\.com(?:/|$)`)
	catRe     = regexp.MustCompile(`[?&]cat=(\d+)`)
	specRe    = regexp.MustCompile(`◇([^：:◇]+)[：:]([^◇\n]*)`)
	dateRe    = regexp.MustCompile(`(\d{4})/(\d{1,2})/(\d{1,2})\s*発売`)
	minRe     = regexp.MustCompile(`(\d+)\s*分`)
	coverRe   = regexp.MustCompile(`<a href="([^"]+\.(?:jpe?g|png))"`)
	imgRe     = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	brRe      = regexp.MustCompile(`(?i)<br\s*/?>`)
	paraRe    = regexp.MustCompile(`(?i)</p>`)
	stripRe   = regexp.MustCompile(`<[^>]+>`)
	splitRe   = regexp.MustCompile(`[・、,／/]`)
	skipCats  = map[int]bool{1: true, 9: true, 10: true}
	commentRe = regexp.MustCompile(`☆作品コメント\s*([\s\S]*?)(?:☆サンプル画像|$)`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(90 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"milu-av.com", "milu-av.com/?cat={id}"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type post struct {
	ID         int    `json:"id"`
	DateGMT    string `json:"date_gmt"`
	Link       string `json:"link"`
	Categories []int  `json:"categories"`
	Title      struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Content struct {
		Rendered string `json:"rendered"`
	} `json:"content"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	filter := ""
	if m := catRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping category %s", siteID, m[1])
		filter = "&categories=" + m[1]
	}

	cats, err := s.categories(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/?rest_route=/wp/v2/posts&per_page=%d&page=%d&orderby=date&order=desc&_fields=id,date_gmt,link,title,content,categories%s", siteBase, perPage, page, filter)
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
			if sc, ok := toScene(p, cats, studioURL, now); ok {
				scenes = append(scenes, sc)
			}
		}
		res := scraper.PageResult{Scenes: scenes, Done: len(posts) < perPage || (pages > 0 && page >= pages)}
		if page == 1 {
			res.Total = total
		}
		return res, nil
	})
}

func (s *Scraper) categories(ctx context.Context) (map[int]string, error) {
	var cs []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	u := siteBase + "/?rest_route=/wp/v2/categories&per_page=100&_fields=id,name"
	if _, _, err := s.getJSON(ctx, u, &cs); err != nil {
		return nil, err
	}
	m := make(map[int]string, len(cs))
	for _, c := range cs {
		m[c.ID] = strings.TrimSpace(html.UnescapeString(c.Name))
	}
	return m, nil
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

// toScene reports false for posts that are not releases: news and event
// announcements carry no 品番 and sit only in the default category.
func toScene(p post, cats map[int]string, studioURL string, now time.Time) (models.Scene, bool) {
	body := p.Content.Rendered
	text := plain(body)
	specs := map[string]string{}
	for _, m := range specRe.FindAllStringSubmatch(text, -1) {
		specs[strings.TrimSpace(m[1])] = strings.TrimSpace(m[2])
	}

	var tags []string
	for _, id := range p.Categories {
		if name := cats[id]; name != "" && !skipCats[id] {
			tags = append(tags, name)
		}
	}
	code := specs["品番"]
	if code == "" && len(tags) == 0 {
		return models.Scene{}, false
	}

	sc := models.Scene{
		ID:        strconv.Itoa(p.ID),
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     strings.Join(strings.Fields(html.UnescapeString(p.Title.Rendered)), " "),
		URL:       p.Link,
		Studio:    studioName,
		Tags:      tags,
		Director:  specs["監督名"],
		ScrapedAt: now,
	}
	if code != "" {
		sc.ID = code
	}
	if t, err := time.Parse("2006-01-02T15:04:05", p.DateGMT); err == nil {
		sc.Date = t.UTC()
	}
	if d := dateRe.FindStringSubmatch(text); d != nil {
		y, _ := strconv.Atoi(d[1])
		mo, _ := strconv.Atoi(d[2])
		da, _ := strconv.Atoi(d[3])
		sc.Date = time.Date(y, time.Month(mo), da, 0, 0, 0, 0, time.UTC)
	}
	if d := minRe.FindStringSubmatch(specs["収録時間"]); d != nil {
		n, _ := strconv.Atoi(d[1])
		sc.Duration = n * 60
	}
	for _, name := range splitRe.Split(specs["女優名"], -1) {
		if name = strings.TrimSpace(name); name != "" {
			sc.Performers = append(sc.Performers, name)
		}
	}
	if m := commentRe.FindStringSubmatch(text); m != nil {
		sc.Description = strings.TrimSpace(m[1])
	} else if code == "" {
		sc.Description = strings.TrimSpace(text)
	}
	if m := coverRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = m[1]
	} else if m := imgRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = m[1]
	}
	return sc, true
}

func plain(s string) string {
	s = brRe.ReplaceAllString(s, "\n")
	s = paraRe.ReplaceAllString(s, "\n")
	s = html.UnescapeString(stripRe.ReplaceAllString(s, ""))
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
