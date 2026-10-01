// Package muku scrapes Muku (muku.tv), a Japanese label whose catalogue is
// reachable only through its kana actress index. See docs/scrapers.md.
package muku

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID         = "muku"
	studioName     = "Muku"
	siteBase       = "https://muku.tv"
	defaultWorkers = 4
)

// kanaIndexes are the ten rows of the actress index; the site offers no other
// way to reach the back catalogue.
var kanaIndexes = []string{"a", "ka", "sa", "ta", "na", "ha", "ma", "ya", "ra", "wa"}

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?muku\.tv(?:/|$)`)
	workRe     = regexp.MustCompile(`/works/detail/([A-Z0-9]+)`)
	actressRe  = regexp.MustCompile(`/actress/detail/(\d+)`)
	titleRe    = regexp.MustCompile(`<title>\s*(.*?)\s*(?:\||</title>)`)
	rowHeadRe  = regexp.MustCompile(`<div class="th">([^<]+)</div>`)
	linkTextRe = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	minutesRe  = regexp.MustCompile(`(\d+)\s*分`)
	dateRe     = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
	ogImageRe  = regexp.MustCompile(`<meta property="og:image" content="([^"]*)"`)
	descRe     = regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"muku.tv", "muku.tv/works/list/release/", "muku.tv/actress/detail/{id}"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	var codes []string
	if m := actressRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "muku: scraping actress %s", m[1])
		codes = s.codesFrom(ctx, fmt.Sprintf("%s/actress/detail/%s", s.base, m[1]))
	} else {
		codes = s.catalogue(ctx, opts)
	}
	if len(codes) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(s.base, fmt.Errorf("no works found"))))
		return
	}
	if !send(ctx, out, scraper.Progress(len(codes))) {
		return
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	now := time.Now().UTC()
	s.fetchWorks(ctx, codes, studioURL, workers, opts.Delay, now, out)
}

// catalogue collects every work code: the release list for the newest, then
// each actress page reached through the kana index for the rest.
func (s *Scraper) catalogue(ctx context.Context, opts scraper.ListOpts) []string {
	seen := map[string]bool{}
	var codes []string
	add := func(found []string) {
		for _, c := range found {
			if !seen[c] {
				seen[c] = true
				codes = append(codes, c)
			}
		}
	}

	add(s.codesFrom(ctx, s.base+"/works/list/release/"))

	actresses := map[string]bool{}
	for _, kana := range kanaIndexes {
		if ctx.Err() != nil {
			return codes
		}
		body, err := s.fetchPage(ctx, s.base+"/actress/"+kana)
		if err != nil {
			continue
		}
		for _, m := range actressRe.FindAllStringSubmatch(body, -1) {
			actresses[m[1]] = true
		}
		delay(ctx, opts.Delay)
	}
	scraper.Debugf(1, "muku: %d actresses in the kana index", len(actresses))

	for id := range actresses {
		if ctx.Err() != nil {
			return codes
		}
		add(s.codesFrom(ctx, fmt.Sprintf("%s/actress/detail/%s", s.base, id)))
		delay(ctx, opts.Delay)
	}
	scraper.Debugf(1, "muku: %d works in the catalogue", len(codes))
	return codes
}

func (s *Scraper) codesFrom(ctx context.Context, pageURL string) []string {
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var codes []string
	for _, m := range workRe.FindAllStringSubmatch(body, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		codes = append(codes, m[1])
	}
	return codes
}

func (s *Scraper) fetchWorks(ctx context.Context, codes []string, studioURL string, workers int, pause time.Duration, now time.Time, out chan<- scraper.SceneResult) {
	work := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for code := range work {
				delay(ctx, pause)
				pageURL := fmt.Sprintf("%s/works/detail/%s", s.base, code)
				body, err := s.fetchPage(ctx, pageURL)
				if err != nil {
					send(ctx, out, scraper.Error(err))
					continue
				}
				scene, err := parseWork(body, code, studioURL, now)
				if err != nil {
					send(ctx, out, scraper.Error(scraper.ParseError(pageURL, err)))
					continue
				}
				if !send(ctx, out, scraper.Scene(scene)) {
					return
				}
			}
		}()
	}
	for _, code := range codes {
		select {
		case work <- code:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return
		}
	}
	close(work)
	wg.Wait()
}

// parseWork reads the release's own metadata table: 発売日 (date), 収録時間
// (runtime, in minutes), 女優 (cast), ジャンル (genres) and シリーズ (series).
func parseWork(body, code, studioURL string, now time.Time) (models.Scene, error) {
	title := ""
	if m := titleRe.FindStringSubmatch(body); m != nil {
		title = cleanText(m[1])
	}
	if title == "" {
		return models.Scene{}, fmt.Errorf("no title on the page")
	}

	scene := models.Scene{
		ID:        code,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     title,
		URL:       siteBase + "/works/detail/" + code,
		ScrapedAt: now,
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := ogImageRe.FindStringSubmatch(body); m != nil {
		scene.Thumbnail = html.UnescapeString(m[1])
	}

	for label, cell := range rows(body) {
		switch label {
		case "発売日":
			if d := dateRe.FindStringSubmatch(cell); d != nil {
				y, _ := strconv.Atoi(d[1])
				mo, _ := strconv.Atoi(d[2])
				day, _ := strconv.Atoi(d[3])
				scene.Date = time.Date(y, time.Month(mo), day, 0, 0, 0, 0, time.UTC)
			}
		case "収録時間":
			if mm := minutesRe.FindStringSubmatch(cell); mm != nil {
				if mins, err := strconv.Atoi(mm[1]); err == nil {
					scene.Duration = mins * 60
				}
			}
		case "女優":
			scene.Performers = linkTexts(cell)
		case "ジャンル":
			scene.Tags = linkTexts(cell)
		case "シリーズ":
			if series := linkTexts(cell); len(series) > 0 {
				scene.Series = series[0]
			}
		}
	}
	return scene, nil
}

// rows maps each table label to the markup between it and the next label.
// Matching the closing boundary with a regex instead consumes the next header,
// which silently drops every other row — RE2 has no lookahead.
func rows(body string) map[string]string {
	heads := rowHeadRe.FindAllStringSubmatchIndex(body, -1)
	out := make(map[string]string, len(heads))
	for i, h := range heads {
		label := strings.TrimSpace(body[h[2]:h[3]])
		end := len(body)
		if i+1 < len(heads) {
			end = heads[i+1][0]
		}
		out[label] = body[h[1]:end]
	}
	return out
}

func linkTexts(cell string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range linkTextRe.FindAllStringSubmatch(cell, -1) {
		v := cleanText(m[1])
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

func delay(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) (string, error) {
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

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}
