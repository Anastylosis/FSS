// Package rebecca scrapes rebecca-web.com, the REbecca Japanese label shop.
package rebecca

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
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
	siteID         = "rebecca"
	studioName     = "REbecca"
	defaultBase    = "http://rebecca-web.com"
	pageSize       = 30
	defaultWorkers = 8
)

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(45 * time.Second), base: defaultBase}
}

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"rebecca-web.com",
		"rebecca-web.com/search.php",
		"rebecca-web.com/search.php?label_id={id}",
		"rebecca-web.com/search.php?series_id={id}",
		"rebecca-web.com/search.php?costume_id={id}",
		"rebecca-web.com/search.php?type_id={id}",
		"rebecca-web.com/actor.php?id={id}",
	}
}

var urlRe = regexp.MustCompile(`^https?://(?:www\.)?rebecca-web\.com(?:[/?#]|$)`)

func (s *Scraper) MatchesURL(u string) bool { return urlRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- listing ----

type card struct {
	id        string
	title     string
	performer string
	thumb     string
}

var (
	cardRe  = regexp.MustCompile(`(?s)<li>\s*<a href="/?detail\.php\?id=([^"&]+)"[^>]*>(.*?)</a>\s*</li>`)
	imgRe   = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	titleRe = regexp.MustCompile(`(?s)<div class="title"[^>]*>(.*?)</div>`)
	actorRe = regexp.MustCompile(`(?s)<div class="actress"[^>]*>(.*?)</div>`)
)

func parseCards(body string) []card {
	var out []card
	for _, m := range cardRe.FindAllStringSubmatch(body, -1) {
		c := card{id: m[1]}
		if t := titleRe.FindStringSubmatch(m[2]); t != nil {
			c.title = cleanText(t[1])
		}
		if a := actorRe.FindStringSubmatch(m[2]); a != nil {
			c.performer = cleanText(a[1])
		}
		// Unreleased works show a shared placeholder, which is not a cover.
		if i := imgRe.FindStringSubmatch(m[2]); i != nil && strings.Contains(i[1], "/media/videos/") {
			c.thumb = html.UnescapeString(i[1])
		}
		out = append(out, c)
	}
	return out
}

type listing struct {
	path  string
	query url.Values
	paged bool
}

func parseListing(studioURL string) listing {
	l := listing{path: "/search.php", query: url.Values{}, paged: true}
	u, err := url.Parse(studioURL)
	if err != nil {
		return l
	}
	if strings.HasSuffix(u.Path, "/actor.php") {
		l.path = "/actor.php"
		l.query.Set("id", u.Query().Get("id"))
		l.paged = false
		return l
	}
	if strings.HasSuffix(u.Path, "/search.php") {
		l.query = u.Query()
		l.query.Del("page")
	}
	return l
}

func (l listing) url(base string, page int) string {
	q := url.Values{}
	for k, v := range l.query {
		q[k] = v
	}
	if l.paged && page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	u := base + l.path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (s *Scraper) get(ctx context.Context, u string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ---- run ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	l := parseListing(studioURL)
	scraper.Debugf(1, "rebecca: scraping %s", l.url(s.base, 1))
	workers := scraper.WorkerCount(opts, defaultWorkers)
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := l.url(s.base, page)
		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseCards(body)
		// Past the last page the site serves a default page rather than a 404,
		// so a page of already-seen works is the end.
		var fresh []card
		for _, c := range cards {
			if !seen[c.id] {
				seen[c.id] = true
				fresh = append(fresh, c)
			}
		}
		if len(fresh) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("no works on the first page"))
			}
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.fetchDetails(ctx, fresh, studioURL, workers, opts, out)
		return scraper.PageResult{
			Scenes:   scenes,
			Done:     !l.paged || len(cards) < pageSize,
			Continue: true,
		}, nil
	})
}

func (s *Scraper) fetchDetails(ctx context.Context, cards []card, studioURL string, workers int, opts scraper.ListOpts, out chan<- scraper.SceneResult) []models.Scene {
	scraper.Debugf(1, "rebecca: fetching %d details with %d workers", len(cards), workers)
	results := make([]*models.Scene, len(cards))
	errs := make([]error, len(cards))

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, len(cards)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				u := s.base + "/detail.php?id=" + url.QueryEscape(cards[i].id)
				body, err := s.get(ctx, u)
				if err != nil {
					errs[i] = err
					continue
				}
				sc, err := parseDetail(body, cards[i], u, studioURL)
				if err != nil {
					errs[i] = err
					continue
				}
				results[i] = sc
			}
		}()
	}
	func() {
		defer close(jobs)
		for i := range cards {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()

	var scenes []models.Scene
	for i, sc := range results {
		if errs[i] != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("rebecca: %s: %w", cards[i].id, errs[i])):
			case <-ctx.Done():
				return scenes
			}
			continue
		}
		if sc != nil {
			scenes = append(scenes, *sc)
		}
	}
	return scenes
}

// ---- detail ----

var (
	specRe    = regexp.MustCompile(`(?s)class="specbox">(.*?)(?:class="modelprofielbox"|class="sampleimagebox"|class="reviewbox"|class="relebant"|\z)`)
	rowRe     = regexp.MustCompile(`(?s)<li>\s*<div class="label">([^<]*)</div>\s*<div class="data">(.*?)</div>\s*</li>`)
	codeRe    = regexp.MustCompile(`(?s)品番</div>\s*<div class="data">(?:\s*<ul>\s*<li>\s*<div class="label">[^<]*</div>\s*<div class="data">)?\s*([^<\s][^<]*)`)
	descRe    = regexp.MustCompile(`(?s)<div class="description">(.*?)</div>`)
	h1Re      = regexp.MustCompile(`(?s)<div class="title">\s*<h1>(.*?)</h1>`)
	anchorRe  = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	brRe      = regexp.MustCompile(`(?i)<br\s*/?>`)
	tagRe     = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe   = regexp.MustCompile(`[ \t\x{a0}\x{3000}]+`)
	minutesRe = regexp.MustCompile(`(\d+)\s*分`)
	isoDateRe = regexp.MustCompile(`(\d{4})[-/](\d{1,2})[-/](\d{1,2})`)
)

func parseDetail(body string, c card, pageURL, studioURL string) (*models.Scene, error) {
	spec := specRe.FindStringSubmatch(body)
	if spec == nil {
		return nil, scraper.ParseError(pageURL, fmt.Errorf("no specbox"))
	}
	text := map[string]string{}
	links := map[string][]string{}
	for _, m := range rowRe.FindAllStringSubmatch(spec[1], -1) {
		label := cleanText(m[1])
		text[label] = cleanText(m[2])
		links[label] = anchorTexts(m[2])
	}

	sc := &models.Scene{
		ID:        c.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		URL:       pageURL,
		Title:     c.title,
		Thumbnail: c.thumb,
		ScrapedAt: time.Now().UTC(),
	}
	// The catalogue number is how a release is identified elsewhere; the
	// shop id is the fallback for download-only works that have none.
	if m := codeRe.FindStringSubmatch(spec[1]); m != nil {
		if code := cleanText(m[1]); code != "" {
			sc.ID = code
		}
	}
	if sc.Title == "" {
		if m := h1Re.FindStringSubmatch(body); m != nil {
			sc.Title = cleanText(m[1])
		}
	}
	if sc.Title == "" {
		sc.Title = text["商品名"]
	}
	if sc.Title == "" {
		return nil, scraper.ParseError(pageURL, fmt.Errorf("no title"))
	}
	if v := links["モデル名"]; len(v) > 0 {
		sc.Performers = v
	} else if c.performer != "" {
		sc.Performers = []string{c.performer}
	}
	if m := isoDateRe.FindStringSubmatch(text["DVD/BD発売日"]); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		sc.Date = time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	}
	if m := minutesRe.FindStringSubmatch(text["収録時間"]); m != nil {
		mins, _ := strconv.Atoi(m[1])
		sc.Duration = mins * 60
	}
	if v := links["シリーズ"]; len(v) > 0 {
		sc.Series = v[0]
	}
	if v := links["レーベル"]; len(v) > 0 {
		sc.Categories = []string{v[0]}
	}
	for _, label := range []string{"タイプ", "コスチューム", "シチュエーション"} {
		sc.Tags = append(sc.Tags, links[label]...)
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = paragraphs(m[1])
	}
	return sc, nil
}

func anchorTexts(seg string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range anchorRe.FindAllStringSubmatch(seg, -1) {
		v := cleanText(m[1])
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func paragraphs(s string) string {
	s = brRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	var lines []string
	for _, ln := range strings.Split(s, "\n") {
		if ln = strings.TrimSpace(spaceRe.ReplaceAllString(ln, " ")); ln != "" {
			lines = append(lines, ln)
		}
	}
	return strings.Join(lines, "\n")
}

func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(spaceRe.ReplaceAllString(s, " ")), " ")
}
