// Package salierixxx scrapes salierixxx.com, a single-page site whose video
// listing is served as an HTML fragment (pages/section/video) behind a
// click-through age disclaimer. See docs/scrapers.md.
package salierixxx

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const siteID = "salierixxx"

// siteBase is overridden by tests.
var siteBase = "https://salierixxx.com"

var (
	matchRe     = regexp.MustCompile(`^https?://(?:www\.)?salierixxx\.com(?:/|$)`)
	filterRe    = regexp.MustCompile(`[?&](genere|categoria)=(\d+)`)
	cardSplit   = regexp.MustCompile(`<div class="programma[^"]*">`)
	idRe        = regexp.MustCompile(`pages/video/(\d+)/([\w-]+)`)
	titleRe     = regexp.MustCompile(`class="hp_box_scene_title_sc"[^>]*>([^<]*)<`)
	thumbRe     = regexp.MustCompile(`<img src="([^"]*uploads/content/[^"]+)"`)
	actorsRe    = regexp.MustCompile(`(?s)class="hp_box_scene_actors_sc"[^>]*>(.*?)</div>`)
	actorRe     = regexp.MustCompile(`<a [^>]*>([^<]+)</a>`)
	dateRe      = regexp.MustCompile(`fa-calendar"></i>\s*(\d{2}/\d{2}/\d{4})`)
	enterRe     = regexp.MustCompile(`href="(\?accept=[^"]+)"`)
	minFullPage = 20
)

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	c := httpx.NewClient(60 * time.Second)
	c.Jar, _ = cookiejar.New(nil)
	return &Scraper{client: c}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"salierixxx.com",
		"salierixxx.com/en/",
		"salierixxx.com/en/#!pages/section/video/?genere={id}",
		"salierixxx.com/en/#!pages/section/video/?categoria={id}",
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
	filter := ""
	if m := filterRe.FindStringSubmatch(studioURL); m != nil {
		filter = m[1] + "=" + m[2] + "&"
		scraper.Debugf(1, "salierixxx: filtered listing %s=%s", m[1], m[2])
	}
	// Loading the page first is what pins the session to English; a bare
	// fragment request is answered in Italian.
	if _, err := s.fetch(ctx, siteBase+"/en/"); err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/en/pages/section/video/?%spage=%d", siteBase, filter, page)
		body, err := s.fetch(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := s.parse(body, studioURL, now)
		if len(scenes) == 0 && page == 1 {
			return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("no videos listed"))
		}
		return scraper.PageResult{Scenes: scenes, Done: len(scenes) < minFullPage}, nil
	})
}

// fetch loads a page fragment, confirming the age disclaimer the way a
// browser does (following the Enter link, with the cookie jar keeping the
// session) if the site redirects there.
func (s *Scraper) fetch(ctx context.Context, u string) (string, error) {
	for attempt := 0; ; attempt++ {
		resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: s.headers()})
		if err != nil {
			return "", err
		}
		b, err := httpx.ReadBody(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return "", err
		}
		if !strings.HasSuffix(resp.Request.URL.Path, "/disclaimer.php") {
			return string(b), nil
		}
		m := enterRe.FindSubmatch(b)
		if m == nil || attempt > 0 {
			return "", scraper.ParseError(u, fmt.Errorf("age disclaimer has no usable Enter link"))
		}
		enter, err := resp.Request.URL.Parse(html.UnescapeString(string(m[1])))
		if err != nil {
			return "", scraper.ParseError(u, err)
		}
		scraper.Debugf(1, "salierixxx: confirming age via %s", enter.Path)
		er, err := httpx.Do(ctx, s.client, httpx.Request{URL: enter.String(), Headers: s.headers()})
		if err != nil {
			return "", err
		}
		_ = er.Body.Close()
	}
}

func (s *Scraper) headers() map[string]string {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	h["X-Requested-With"] = "XMLHttpRequest"
	return h
}

func (s *Scraper) parse(body, studioURL string, now time.Time) []models.Scene {
	var scenes []models.Scene
	seen := map[string]bool{}
	cards := cardSplit.Split(body, -1)
	if len(cards) < 2 {
		return nil
	}
	for _, card := range cards[1:] {
		id := idRe.FindStringSubmatch(card)
		title := titleRe.FindStringSubmatch(card)
		if id == nil || title == nil || seen[id[1]] {
			continue
		}
		seen[id[1]] = true
		sc := models.Scene{
			ID:        id[1],
			SiteID:    siteID,
			StudioURL: studioURL,
			Title:     clean(title[1]),
			URL:       siteBase + "/en/#!pages/video/" + id[1] + "/" + id[2],
			Studio:    "Salieri XXX",
			ScrapedAt: now,
		}
		if m := thumbRe.FindStringSubmatch(card); m != nil {
			sc.Thumbnail = html.UnescapeString(m[1])
		}
		if m := actorsRe.FindStringSubmatch(card); m != nil {
			for _, a := range actorRe.FindAllStringSubmatch(m[1], -1) {
				if name := clean(a[1]); name != "" {
					sc.Performers = append(sc.Performers, name)
				}
			}
		}
		if m := dateRe.FindStringSubmatch(card); m != nil {
			if d, err := time.Parse("02/01/2006", m[1]); err == nil {
				sc.Date = d.UTC()
			}
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func clean(s string) string { return strings.Join(strings.Fields(html.UnescapeString(s)), " ") }
