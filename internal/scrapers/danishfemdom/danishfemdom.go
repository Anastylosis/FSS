// Package danishfemdom scrapes the free-trailer listing of danishfemdom.com.
package danishfemdom

import (
	"context"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "danishfemdom"
	studioName = "Danish Femdom"
)

var siteBase = "https://www.danishfemdom.com"

type Scraper struct {
	client *http.Client
}

func New() *Scraper { return &Scraper{client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"danishfemdom.com",
		"danishfemdom.com/en/trailers/",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?danishfemdom\.com(?:/(?:en/?(?:trailers/?)?)?)?(?:[?#].*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.Debugf(1, "danishfemdom: fetching the single-page trailer listing")

	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: siteBase + "/en/trailers/", Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	scenes := parseListing(body, studioURL, time.Now().UTC())
	if len(scenes) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(siteBase+"/en/trailers/", errNoCards)))
		return
	}
	if !send(ctx, out, scraper.Progress(len(scenes))) {
		return
	}
	for _, sc := range scenes {
		if !send(ctx, out, scraper.Scene(sc)) {
			return
		}
	}
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

type noCardsError struct{}

func (noCardsError) Error() string { return "no trailer cards found" }

var errNoCards = noCardsError{}

var (
	cardSplitRe = regexp.MustCompile(`<div class="col-3-6 dfcustomsearch"`)
	fnRe        = regexp.MustCompile(`href="/en/trailers/video/\?fn=(\d+)"`)
	thumbRe     = regexp.MustCompile(`<img class="img1" data-preview-src="([^"]*)" src="([^"]+)"`)
	durRe       = regexp.MustCompile(`<span style="float:right">([\d.:]+)</span>`)
	titleRe     = regexp.MustCompile(`<h3>(.*?)</h3>`)
	descRe      = regexp.MustCompile(`(?s)</a>\s*(?:<span>[^<]*</span>\s*<br\s*/?>\s*)?<p>(.*?)</p>`)
)

func parseListing(body []byte, studioURL string, now time.Time) []models.Scene {
	var out []models.Scene
	seen := map[string]bool{}
	parts := cardSplitRe.Split(string(body), -1)
	for _, blk := range parts[1:] {
		if i := strings.Index(blk, "</article>"); i >= 0 {
			blk = blk[:i]
		}
		fn := fnRe.FindStringSubmatch(blk)
		t := titleRe.FindStringSubmatch(blk)
		if fn == nil || t == nil || seen[fn[1]] {
			continue
		}
		seen[fn[1]] = true
		sc := models.Scene{
			ID:        fn[1],
			SiteID:    siteID,
			StudioURL: studioURL,
			Title:     strings.Join(strings.Fields(html.UnescapeString(t[1])), " "),
			URL:       siteBase + "/en/trailers/video/?fn=" + fn[1],
			Studio:    studioName,
			ScrapedAt: now,
		}
		if m := thumbRe.FindStringSubmatch(blk); m != nil {
			sc.Thumbnail = siteBase + m[2]
			if m[1] != "" {
				sc.Preview = siteBase + m[1]
			}
		}
		if m := durRe.FindStringSubmatch(blk); m != nil {
			sc.Duration = parseutil.ParseDurationColon(strings.ReplaceAll(m[1], ".", ":"))
		}
		if m := descRe.FindStringSubmatch(blk); m != nil {
			sc.Description = strings.Join(strings.Fields(html.UnescapeString(m[1])), " ")
		}
		out = append(out, sc)
	}
	return out
}
