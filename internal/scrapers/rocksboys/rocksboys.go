// Package rocksboys scrapes rocksboys.com, a Laravel tour whose public scene
// listing is the JSON feed behind its infinite scroll (/ajax/scenes), reached
// after the site's "I am 18 or older" form. See docs/scrapers.md.
package rocksboys

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID   = "rocksboys"
	pageSize = 50
)

// siteBase is overridden by tests.
var siteBase = "https://www.rocksboys.com"

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?rocksboys\.com(?:/|$)`)
	formRe  = regexp.MustCompile(`(?is)<form[^>]*action="([^"]*age-gate[^"]*)"[^>]*>(.*?)</form>`)
	inputRe = regexp.MustCompile(`(?is)<input\b[^>]*>`)
	attrRe  = regexp.MustCompile(`(?i)\b(name|value)="([^"]*)"`)
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
	return []string{"rocksboys.com", "rocksboys.com/scenes"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type apiScene struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Brief       string   `json:"brief"`
	PublishDate string   `json:"publish_date"`
	Runtime     string   `json:"runtime"`
	Poster      string   `json:"poster_url"`
	Quality     string   `json:"quality"`
	Models      []string `json:"models"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	if err := s.confirmAge(ctx); err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/ajax/scenes?offset=%d&limit=%d&sort=recent", siteBase, (page-1)*pageSize, pageSize)
		resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, siteBase)})
		if err != nil {
			return scraper.PageResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		var items []apiScene
		if err := httpx.DecodeJSON(resp.Body, &items); err != nil {
			return scraper.PageResult{}, scraper.ParseError(u, err)
		}
		scenes := make([]models.Scene, 0, len(items))
		for _, it := range items {
			scenes = append(scenes, toScene(it, studioURL, now))
		}
		return scraper.PageResult{Scenes: scenes, Done: len(items) < pageSize}, nil
	})
}

// confirmAge submits the site's age-gate form, hidden token included, the way
// a visitor's browser does; the cookie jar keeps the resulting cookie.
func (s *Scraper) confirmAge(ctx context.Context) error {
	scraper.Debugf(1, "rocksboys: loading %s", siteBase)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: siteBase + "/", Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return err
	}
	b, err := httpx.ReadBody(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	m := formRe.FindSubmatch(b)
	if m == nil {
		return nil
	}
	form := url.Values{}
	for _, in := range inputRe.FindAllString(string(m[2]), -1) {
		var name, value string
		for _, a := range attrRe.FindAllStringSubmatch(in, -1) {
			if strings.EqualFold(a[1], "name") {
				name = a[2]
			} else {
				value = html.UnescapeString(a[2])
			}
		}
		if name != "" {
			form.Set(name, value)
		}
	}
	action, err := resp.Request.URL.Parse(html.UnescapeString(string(m[1])))
	if err != nil {
		return scraper.ParseError(siteBase, err)
	}
	scraper.Debugf(1, "rocksboys: confirming age via %s", action.Path)
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	h["Content-Type"] = "application/x-www-form-urlencoded"
	h["Referer"] = siteBase + "/"
	pr, err := httpx.Do(ctx, s.client, httpx.Request{Method: http.MethodPost, URL: action.String(), Body: []byte(form.Encode()), Headers: h, Idempotent: true})
	if err != nil {
		return err
	}
	_ = pr.Body.Close()
	return nil
}

func toScene(it apiScene, studioURL string, now time.Time) models.Scene {
	id := fmt.Sprint(it.ID)
	sc := models.Scene{
		ID:          id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       clean(it.Name),
		URL:         siteBase + "/scene/" + id,
		Description: clean(it.Brief),
		Thumbnail:   it.Poster,
		Studio:      "RocksBoys",
		Performers:  it.Models,
		Duration:    parseutil.ParseDurationColon(it.Runtime),
		Resolution:  it.Quality,
		ScrapedAt:   now,
	}
	if d, err := time.Parse("2006-01-02", it.PublishDate); err == nil {
		sc.Date = d.UTC()
	}
	return sc
}

func clean(s string) string { return strings.Join(strings.Fields(html.UnescapeString(s)), " ") }
