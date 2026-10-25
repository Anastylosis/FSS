// Package shemaleclub scrapes the Shemale Club tour (shemale-club.com/tour),
// the TGirl Network's long-running multi-model site. It runs its own CMS, not
// the network tour template, so it is separate from the tgirlnetwork package.
package shemaleclub

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
	siteID         = "shemaleclub"
	domain         = "shemale-club.com"
	defaultBase    = "https://shemale-club.com"
	studioName     = "Shemale Club"
	moviesCategory = "5"
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

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?shemale-club\.com(?:/|$)`)
	modelURLRe = regexp.MustCompile(`/sets\.php\?(?:[^#]*&)?id=(\d+)`)
	catURLRe   = regexp.MustCompile(`/category\.php\?(?:[^#]*&)?id=(\d+)`)
)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		domain,
		domain + "/tour/",
		domain + "/tour/category.php?id={id}",
		domain + "/tour/sets.php?id={id}",
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

	var listPath string
	if m := modelURLRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping model %s", siteID, m[1])
		listPath = "/tour/sets.php?id=" + m[1]
	} else {
		cat := moviesCategory
		if m := catURLRe.FindStringSubmatch(studioURL); m != nil {
			cat = m[1]
		}
		scraper.Debugf(1, "%s: scraping category %s", siteID, cat)
		listPath = "/tour/category.php?id=" + cat
	}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := s.base + listPath
		if page > 1 {
			u += "&page=" + strconv.Itoa(page)
		}
		body, err := s.fetch(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		page1 := string(body)
		cards, items := parseCards(page1)
		if items == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("no update cards on the first listing page"))
			}
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.buildScenes(ctx, studioURL, cards, opts, out)
		return scraper.PageResult{
			Scenes:   scenes,
			Done:     !hasPageLink(page1, page+1),
			Continue: true,
		}, nil
	})
}

// hasPageLink reports whether the pager links to page n. The pager always
// carries a next-arrow link, so its absence is the end of the listing.
func hasPageLink(body string, n int) bool {
	want := strconv.Itoa(n)
	for _, m := range pageLinkRe.FindAllStringSubmatch(body, -1) {
		if m[1] == want {
			return true
		}
	}
	return false
}

var pageLinkRe = regexp.MustCompile(`[?&]page=(\d+)`)

// buildScenes fetches each new card's detail page for performers and tags. A
// failed detail keeps the card-only scene rather than dropping it.
func (s *Scraper) buildScenes(ctx context.Context, studioURL string, cards []card, opts scraper.ListOpts, out chan<- scraper.SceneResult) []models.Scene {
	now := time.Now().UTC()
	scenes := make([]models.Scene, len(cards))
	for i, c := range cards {
		scenes[i] = models.Scene{
			ID:        c.id,
			SiteID:    siteID,
			StudioURL: studioURL,
			Title:     c.title,
			URL:       s.base + "/tour/index.php?id=" + c.id,
			Thumbnail: s.absolute(c.thumb),
			Duration:  c.duration,
			Studio:    studioName,
			ScrapedAt: now,
		}
	}

	workers := scraper.WorkerCount(opts, defaultWorkers)
	idx := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				if err := s.enrich(ctx, &scenes[i]); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		}()
	}
feed:
	for i, c := range cards {
		if opts.KnownIDs[c.id] {
			continue
		}
		select {
		case idx <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(idx)
	wg.Wait()

	for _, err := range errs {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
			return scenes
		}
	}
	return scenes
}

func (s *Scraper) enrich(ctx context.Context, sc *models.Scene) error {
	body, err := s.fetch(ctx, sc.URL)
	if err != nil {
		return fmt.Errorf("detail %s: %w", sc.ID, err)
	}
	d := parseDetail(string(body))
	if d.title == "" {
		return scraper.ParseError(sc.URL, fmt.Errorf("no title on detail page"))
	}
	sc.Title = d.title
	sc.Performers = d.performers
	sc.Tags = d.tags
	if sc.Duration == 0 {
		sc.Duration = d.duration
	}
	return nil
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		Method:  http.MethodGet,
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

var doubleSlashRe = regexp.MustCompile(`/{2,}`)

func (s *Scraper) absolute(ref string) string {
	switch {
	case ref == "":
		return ""
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return ref
	case strings.HasPrefix(ref, "/"):
		return s.base + doubleSlashRe.ReplaceAllString(ref, "/")
	default:
		return s.base + "/tour/" + doubleSlashRe.ReplaceAllString(ref, "/")
	}
}

// ---- listing ----

type card struct {
	id       string
	title    string
	thumb    string
	duration int
}

var (
	cardSplitRe = regexp.MustCompile(`<div class="col-lg-4 col-md-4 col-sm-6 col-xs-12">`)
	cardLinkRe  = regexp.MustCompile(`index\.php\?id=(\d+)`)
	cardThumbRe = regexp.MustCompile(`background-image:\s*url\(([^)]+)\)`)
	cardCountRe = regexp.MustCompile(`(?s)<div class="pcount">(.*?)</div>`)
	cardTitleRe = regexp.MustCompile(`<a[^>]+title="([^"]*)"[^>]*class="ha"`)
	minutesRe   = regexp.MustCompile(`(\d+)\s*min`)
)

// parseCards returns the movie cards of a listing page and the number of
// update cards of any kind. Model pages mix photo sets in; a movie card is
// the one whose count reads "N min of video".
func parseCards(body string) ([]card, int) {
	var out []card
	items := 0
	seen := make(map[string]bool)
	for _, chunk := range cardSplitRe.Split(body, -1)[1:] {
		m := cardLinkRe.FindStringSubmatch(chunk)
		if m == nil {
			continue
		}
		items++
		count := cleanText(firstSubmatch(cardCountRe, chunk))
		if !strings.Contains(count, "of video") || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		c := card{
			id:    m[1],
			title: cleanText(firstSubmatch(cardTitleRe, chunk)),
			thumb: strings.Trim(firstSubmatch(cardThumbRe, chunk), `'"`),
		}
		if mm := minutesRe.FindStringSubmatch(count); mm != nil {
			n, _ := strconv.Atoi(mm[1])
			c.duration = n * 60
		}
		out = append(out, c)
	}
	return out, items
}

// ---- detail ----

type detail struct {
	title      string
	performers []string
	tags       []string
	duration   int
}

var (
	detailTitleRe    = regexp.MustCompile(`(?s)<h3>(.*?)<span`)
	detailModelRe    = regexp.MustCompile(`<a class="model_category_link"[^>]*>([^<]*)</a>`)
	detailKeywordsRe = regexp.MustCompile(`<meta name="keywords" content="([^"]*)"`)
	detailMinutesRe  = regexp.MustCompile(`(\d+)&nbsp;min&nbsp;of video`)
)

// siteKeywords are the CMS's own category names, which every update carries
// in its keywords; they say nothing about the scene.
var siteKeywords = map[string]bool{
	"all updates": true, "movies": true, "vid": true, "vids": true,
}

func parseDetail(body string) detail {
	d := detail{title: cleanText(firstSubmatch(detailTitleRe, body))}
	isModel := make(map[string]bool)
	for _, m := range detailModelRe.FindAllStringSubmatch(body, -1) {
		name := cleanText(m[1])
		if name == "" || isModel[strings.ToLower(name)] {
			continue
		}
		isModel[strings.ToLower(name)] = true
		d.performers = append(d.performers, name)
	}
	for _, k := range strings.Split(html.UnescapeString(firstSubmatch(detailKeywordsRe, body)), ",") {
		k = strings.TrimSpace(k)
		lk := strings.ToLower(k)
		if k == "" || siteKeywords[lk] || isModel[lk] {
			continue
		}
		d.tags = append(d.tags, k)
	}
	if m := detailMinutesRe.FindStringSubmatch(body); m != nil {
		n, _ := strconv.Atoi(m[1])
		d.duration = n * 60
	}
	return d
}

// ---- helpers ----

var tagStripRe = regexp.MustCompile(`<[^>]*>`)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.Join(strings.Fields(s), " ")
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}
