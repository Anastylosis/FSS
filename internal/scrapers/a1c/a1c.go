// Package a1c scrapes the per-label product pages of A1C (a1c.jp), a
// Japanese anime-AV publisher whose labels (Majin, ChuChu, Collaboration
// Works, Mousou Senka, ...) each keep a static product.html. See
// docs/scrapers.md.
package a1c

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

const siteID = "a1c"

var siteBase = "https://www.a1c.jp"

var (
	matchRe   = regexp.MustCompile(`^https?://(?:www\.)?a1c\.jp/(~[^/?#]+|brandpage/[^/?#]+)`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	itemRe    = regexp.MustCompile(`(?s)<li>\s*<a href="(?:\./)?product/([^"/]+)\.html">(.*?)</li>`)
	imgRe     = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	imgTagRe  = regexp.MustCompile(`<img[^>]*>`)
	dateRe    = regexp.MustCompile(`(\d{4})/(\d{1,2})(?:/(\d{1,2}))?\s*Release`)
	titleRe   = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	brRe      = regexp.MustCompile(`(?i)<br\s*/?>`)
	stripRe   = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"a1c.jp/~{brand}/", "a1c.jp/brandpage/{brand}/"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, _ scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	send := func(r scraper.SceneResult) bool {
		select {
		case out <- r:
			return true
		case <-ctx.Done():
			return false
		}
	}

	m := matchRe.FindStringSubmatch(studioURL)
	if m == nil {
		send(scraper.Error(fmt.Errorf("a1c: unrecognised URL %q", studioURL)))
		return
	}
	brandPath := m[1]
	scraper.Debugf(1, "%s: scraping label %s", siteID, brandPath)

	pageURL := fmt.Sprintf("%s/%s/product.html", siteBase, brandPath)
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: pageURL, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		send(scraper.Error(err))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		send(scraper.Error(err))
		return
	}

	scenes := parse(string(body), siteBase+"/"+brandPath+"/", studioURL, time.Now().UTC())
	if len(scenes) == 0 {
		send(scraper.Error(scraper.ParseError(pageURL, fmt.Errorf("no products found"))))
		return
	}
	if !send(scraper.Progress(len(scenes))) {
		return
	}
	for _, sc := range scenes {
		if !send(scraper.Scene(sc)) {
			return
		}
	}
}

func parse(page, base, studioURL string, now time.Time) []models.Scene {
	label := ""
	if m := titleRe.FindStringSubmatch(page); m != nil {
		for _, part := range strings.Split(html.UnescapeString(m[1]), "|") {
			if part = strings.TrimSpace(part); part != "" && part != "Product" {
				label = part
			}
		}
	}

	page = commentRe.ReplaceAllString(page, "")
	var scenes []models.Scene
	for _, m := range itemRe.FindAllStringSubmatch(page, -1) {
		slug, inner := m[1], m[2]
		sc := models.Scene{
			ID:        slug,
			SiteID:    siteID,
			StudioURL: studioURL,
			URL:       base + "product/" + slug + ".html",
			Studio:    label,
			ScrapedAt: now,
		}
		if im := imgRe.FindStringSubmatch(inner); im != nil {
			sc.Thumbnail = base + strings.TrimPrefix(im[1], "./")
		}
		if d := dateRe.FindStringSubmatch(inner); d != nil {
			y, _ := strconv.Atoi(d[1])
			mo, _ := strconv.Atoi(d[2])
			day := 1
			if d[3] != "" {
				day, _ = strconv.Atoi(d[3])
			}
			sc.Date = time.Date(y, time.Month(mo), day, 0, 0, 0, 0, time.UTC)
			inner = dateRe.ReplaceAllString(inner, "")
		}
		inner = imgTagRe.ReplaceAllString(inner, "")
		inner = brRe.ReplaceAllString(inner, " ")
		sc.Title = strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(inner, " "))), " ")
		if sc.Title == "" {
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
}
