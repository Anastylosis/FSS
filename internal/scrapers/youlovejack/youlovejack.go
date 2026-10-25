// Package youlovejack scrapes youlovejack.com, a static-HTML amateur gay
// studio whose tour lists every scene across numbered index pages.
package youlovejack

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
	"unicode/utf8"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID         = "youlovejack"
	studioName     = "YouLoveJack"
	defaultBase    = "https://www.youlovejack.com"
	listingDir     = "/homemade-gay-porn-videos/"
	defaultWorkers = 4
)

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second), base: defaultBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"youlovejack.com",
		"youlovejack.com/homemade-gay-porn-videos/index.html",
	}
}

var urlRe = regexp.MustCompile(`^https?://(?:www\.)?youlovejack\.com(?:[/?#]|$)`)

func (s *Scraper) MatchesURL(u string) bool { return urlRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- listing ----

type card struct {
	path      string // relative to listingDir, e.g. content/<slug>/<name>.html
	id        string
	performer string
	thumb     string
}

var (
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	nameRe    = regexp.MustCompile(`href="(content/([^/"]+)/[^"]+\.html)"[^>]*class="modelName">([^<]*)<`)
	thumbRe   = regexp.MustCompile(`href="(content/[^"]+\.html)"[^>]*>\s*<img[^>]+src="([^"]+)"`)
)

func pageURL(base string, page int) string {
	if page <= 1 {
		return base + listingDir + "index.html"
	}
	return base + listingDir + "index" + strconv.Itoa(page) + ".html"
}

func parseCards(body, base string) []card {
	thumbs := map[string]string{}
	for _, m := range thumbRe.FindAllStringSubmatch(body, -1) {
		thumbs[m[1]] = m[2]
	}
	var out []card
	seen := map[string]bool{}
	for _, m := range nameRe.FindAllStringSubmatch(body, -1) {
		if seen[m[2]] {
			continue
		}
		seen[m[2]] = true
		c := card{path: m[1], id: m[2], performer: cleanText(m[3])}
		if t := thumbs[m[1]]; t != "" {
			c.thumb = base + "/" + strings.TrimPrefix(t, "../")
		}
		out = append(out, c)
	}
	return out
}

// hasNext reports whether the page links to the following index page. The
// last page carries a commented-out "more videos" square, so comments must
// already be stripped.
func hasNext(body string, page int) bool {
	return strings.Contains(body, `href="index`+strconv.Itoa(page+1)+`.html"`)
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
	return commentRe.ReplaceAllString(latin1ToUTF8(raw), ""), nil
}

// latin1ToUTF8 decodes the site's declared iso-8859-1 when the body is not
// already valid UTF-8.
func latin1ToUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

// ---- run ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := pageURL(s.base, page)
		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseCards(body, s.base)
		if len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("no scenes on listing page %d", page))
		}
		scenes := s.fetchDetails(ctx, cards, studioURL, workers, opts, out)
		return scraper.PageResult{
			Scenes:   scenes,
			Done:     !hasNext(body, page),
			Continue: true,
		}, nil
	})
}

func (s *Scraper) fetchDetails(ctx context.Context, cards []card, studioURL string, workers int, opts scraper.ListOpts, out chan<- scraper.SceneResult) []models.Scene {
	scraper.Debugf(1, "youlovejack: fetching %d details with %d workers", len(cards), workers)
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
				u := s.base + listingDir + cards[i].path
				body, err := s.get(ctx, u)
				if err != nil {
					errs[i] = err
					continue
				}
				results[i], errs[i] = parseDetail(body, cards[i], u, studioURL, s.base)
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
			case out <- scraper.Error(fmt.Errorf("youlovejack: %s: %w", cards[i].id, errs[i])):
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
	h1Re         = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	describeRe   = regexp.MustCompile(`(?s)<p class="videoDescribe">(.*?)</p>`)
	metaDescRe   = regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
	coverRe      = regexp.MustCompile(`<img src="((?:\.\./)*mpreview/[^"]+)"`)
	minutesRe    = regexp.MustCompile(`(\d+) MINUTE`)
	recordRe     = regexp.MustCompile(`href="/2257-Q\.html\?[^"]*&([^"&]+)"`)
	tagRe        = regexp.MustCompile(`(?s)<[^>]*>`)
	andRe        = regexp.MustCompile(`(?i)\s+(?:and|blows|&)\s+`)
	possessiveRe = regexp.MustCompile(`(?i)'s\s+\w+$`)
)

func parseDetail(body string, c card, pageURL, studioURL, base string) (*models.Scene, error) {
	m := h1Re.FindStringSubmatch(body)
	if m == nil || cleanText(m[1]) == "" {
		return nil, scraper.ParseError(pageURL, fmt.Errorf("no title"))
	}
	sc := &models.Scene{
		ID:        c.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		URL:       pageURL,
		Title:     cleanText(m[1]),
		Thumbnail: c.thumb,
		ScrapedAt: time.Now().UTC(),
	}
	sc.Performers = performers(c.performer)
	var paras []string
	for _, p := range describeRe.FindAllStringSubmatch(body, -1) {
		if t := cleanText(p[1]); t != "" {
			paras = append(paras, t)
		}
	}
	if len(paras) > 0 {
		sc.Description = strings.Join(paras, "\n\n")
	} else if d := metaDescRe.FindStringSubmatch(body); d != nil {
		sc.Description = cleanText(d[1])
	}
	if cv := coverRe.FindStringSubmatch(body); cv != nil {
		sc.Thumbnail = base + "/" + strings.TrimLeft(cv[1], "./")
	}
	if d := minutesRe.FindStringSubmatch(body); d != nil {
		n, _ := strconv.Atoi(d[1])
		sc.Duration = n * 60
	}
	// The only date the tour publishes is the 2257 record date in the
	// compliance link's query, e.g. "...&&NOVEMBER 13, 2015".
	if r := recordRe.FindStringSubmatch(body); r != nil {
		if t, err := parseutil.TryParseDate(titleCase(cleanText(r[1])), "January 2, 2006", "January 2 2006"); err == nil {
			sc.Date = t.UTC()
		}
	}
	return sc, nil
}

// performers splits a listing card's caption ("NATHAN AND ROBIN",
// "H BLOWS ANDREW", "TIGGER'S ROOM") into names.
func performers(caption string) []string {
	caption = possessiveRe.ReplaceAllString(caption, "")
	var out []string
	for _, p := range andRe.Split(caption, -1) {
		if p = titleCase(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		// Initials such as "JT" stay upper case.
		if len(w) == 2 && !strings.ContainsAny(w, "aeiouy.'") {
			words[i] = strings.ToUpper(w)
			continue
		}
		r, n := utf8.DecodeRuneInString(w)
		w = strings.ToUpper(string(r)) + w[n:]
		// O'CONNOR -> O'Connor
		if len(w) > 2 && w[1] == '\'' {
			w = w[:2] + strings.ToUpper(w[2:3]) + w[3:]
		}
		words[i] = w
	}
	return strings.Join(words, " ")
}

func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}
