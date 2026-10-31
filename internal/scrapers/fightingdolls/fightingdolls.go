// Package fightingdolls scrapes the fighting-dolls.com movie listing.
package fightingdolls

import (
	"context"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID         = "fightingdolls"
	studioName     = "Fighting Dolls"
	pageSize       = 30
	defaultWorkers = 4
)

var siteBase = "https://www.fighting-dolls.com"

type Scraper struct {
	client *http.Client
}

func New() *Scraper { return &Scraper{client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"fighting-dolls.com",
		"fighting-dolls.com/all-fighting-dolls-videos/",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?fighting-dolls\.com(?:/(?:all-fighting-dolls-videos/?(?:\d+/?)?)?)?(?:[?#].*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "fightingdolls: walking the movie listing, %d detail workers", workers)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.get(ctx, siteBase+"/all-fighting-dolls-videos/"+strconv.Itoa(page-1)+"/")
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		scenes := make([]models.Scene, len(cards))
		for i, c := range cards {
			scenes[i] = c.scene(studioURL, now)
		}
		if err := s.fillDetails(ctx, scenes, cards, opts, workers); err != nil {
			return scraper.PageResult{}, err
		}
		return scraper.PageResult{Scenes: scenes, Done: len(cards) < pageSize}, nil
	})
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func (s *Scraper) fillDetails(ctx context.Context, scenes []models.Scene, cards []card, opts scraper.ListOpts, workers int) error {
	if workers > len(scenes) {
		workers = len(scenes)
	}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		jobs     = make(chan int)
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if !scraper.Pace(ctx, opts.Delay) {
					continue
				}
				body, err := s.get(ctx, siteBase+cards[i].path)
				if err == nil {
					applyDetail(&scenes[i], parseDetail(body))
					continue
				}
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	for i := range scenes {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

type card struct {
	path, num, title, thumb, blurb string
	duration                       int
}

var (
	cardRe      = regexp.MustCompile(`(?s)<div class="card-image">(.*?)<div class="card-download`)
	cardLinkRe  = regexp.MustCompile(`<a href="(/movies/[^"]*?-(\d+)/)" title="([^"]*)"><img src="([^"]+)"`)
	cardMetaRe  = regexp.MustCompile(`<span>[A-Z]*\d+ / ([\d:]+)</span>`)
	cardBlurbRe = regexp.MustCompile(`(?s)<div class="card-section">\s*<p>(.*?)</p>`)
)

func parseListing(body []byte) []card {
	var out []card
	for _, m := range cardRe.FindAllSubmatch(body, -1) {
		blk := string(m[1])
		l := cardLinkRe.FindStringSubmatch(blk)
		if l == nil {
			continue
		}
		c := card{path: l[1], num: l[2], title: html.UnescapeString(l[3]), thumb: siteBase + l[4]}
		if d := cardMetaRe.FindStringSubmatch(blk); d != nil {
			c.duration = parseutil.ParseDurationColon(d[1])
		}
		if b := cardBlurbRe.FindStringSubmatch(blk); b != nil {
			c.blurb = clean(b[1])
		}
		out = append(out, c)
	}
	return out
}

func (c card) scene(studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:          c.num,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       c.title,
		URL:         siteBase + c.path,
		Thumbnail:   c.thumb,
		Description: c.blurb,
		Duration:    c.duration,
		Studio:      studioName,
		ScrapedAt:   now,
	}
}

type detail struct {
	performers []string
	tags       []string
	desc       string
}

var (
	catBlockRe = regexp.MustCompile(`(?s)<div class="categories">(.*?)</div>`)
	catRe      = regexp.MustCompile(`<a href="/category/[^"]*">([^<]+)</a>`)
	girlRe     = regexp.MustCompile(`<h3><a href="/girls/[^"]*" title="([^"]+)"`)
	descRe     = regexp.MustCompile(`(?s)<h3>Movie Description</h3>\s*<hr[^>]*>\s*(.*?)\s*</div>`)
)

func parseDetail(body []byte) detail {
	var d detail
	if b := catBlockRe.FindSubmatch(body); b != nil {
		for _, m := range catRe.FindAllSubmatch(b[1], -1) {
			d.tags = append(d.tags, clean(string(m[1])))
		}
	}
	for _, m := range girlRe.FindAllSubmatch(body, -1) {
		d.performers = append(d.performers, html.UnescapeString(string(m[1])))
	}
	if m := descRe.FindSubmatch(body); m != nil {
		d.desc = clean(string(m[1]))
	}
	return d
}

func applyDetail(sc *models.Scene, d detail) {
	sc.Performers = d.performers
	sc.Tags = d.tags
	if d.desc != "" {
		sc.Description = d.desc
	}
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(s, " "))), " ")
}
