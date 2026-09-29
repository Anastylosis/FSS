package xevbellringer

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
	defaultBase = "https://www.xevunleashed.com"
	siteID      = "xevbellringer"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?xevunleashed\.com(?:/|$)`)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{"xevunleashed.com", "xevunleashed.com/categories/movies.html"}
}
func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, _ string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listingScene)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ls := range work {
				scene, err := s.fetchDetail(ctx, ls, opts.Delay)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(work)
		s.enqueuePages(ctx, opts, out, work)
	}()

	wg.Wait()
}

func (s *Scraper) enqueuePages(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "xevbellringer: fetching page %d", page)

		var pageURL string
		if page == 1 {
			pageURL = s.base + "/categories/movies.html"
		} else {
			pageURL = fmt.Sprintf("%s/categories/movies_%d.html", s.base, page)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		scenes := parseListingPage(body, s.base)
		if len(scenes) == 0 {
			return
		}

		if page == 1 {
			total := estimateTotal(body, len(scenes))
			if total > 0 {
				scraper.Debugf(1, "xevbellringer: %d total scenes", total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
		}

		for _, ls := range scenes {
			if opts.KnownIDs[ls.id] {
				scraper.Debugf(1, "xevbellringer: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- ls:
			case <-ctx.Done():
				return
			}
		}
	}
}

type listingScene struct {
	id    string
	url   string
	title string
	date  time.Time
	thumb string
	price float64
}

var (
	itemStartRe = regexp.MustCompile(`<div class="updateItem">`)
	titleRe     = regexp.MustCompile(`(?s)<h4>\s*<a[^>]+href="([^"]+)"[^>]*>\s*(.*?)\s*</a>`)
	dateRe      = regexp.MustCompile(`<span>(\d{2}/\d{2}/\d{4})</span>`)
	priceRe     = regexp.MustCompile(`Buy \$(\d+(?:\.\d+)?)`)
	thumbRe     = regexp.MustCompile(`src="(content/[^"]+)"`)
	// The site's own pagination links carry a sort suffix — movies_2_d.html,
	// movies_1_z.html — so a pattern anchored on movies_{N}.html matched none
	// of them and every scrape reported a one-page total. The suffixless form
	// is still accepted because that is what this scraper requests.
	maxPageRe = regexp.MustCompile(`/categories/movies_(\d+)(?:_[a-z]+)?\.html`)

	descRe = regexp.MustCompile(`(?s)class="latest_update_description">(.*?)</span>`)
	tagsRe = regexp.MustCompile(`(?s)class="update_tags">(.*?)</span>`)
	tagRe  = regexp.MustCompile(`>([^<]+)</a>`)
)

func slugFromURL(u string) string {
	if i := strings.LastIndexByte(u, '/'); i >= 0 {
		name := u[i+1:]
		name = strings.TrimSuffix(name, ".html")
		return name
	}
	return ""
}

func parseListingPage(body []byte, base string) []listingScene {
	page := string(body)
	starts := itemStartRe.FindAllStringIndex(page, -1)
	scenes := make([]listingScene, 0, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[loc[0]:end]

		var ls listingScene

		if m := titleRe.FindStringSubmatch(block); m != nil {
			href := m[1]
			if !strings.HasPrefix(href, "http") {
				href = base + "/" + strings.TrimLeft(href, "/")
			}
			ls.url = href
			ls.title = strings.TrimSpace(html.UnescapeString(m[2]))
			ls.id = slugFromURL(href)
		}

		if ls.id == "" {
			continue
		}

		if m := dateRe.FindStringSubmatch(block); m != nil {
			if t, err := time.Parse("01/02/2006", m[1]); err == nil {
				ls.date = t.UTC()
			}
		}

		if m := priceRe.FindStringSubmatch(block); m != nil {
			ls.price, _ = strconv.ParseFloat(m[1], 64)
		}

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			ls.thumb = base + "/" + m[1]
		}

		scenes = append(scenes, ls)
	}
	return scenes
}

func estimateTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

type detailData struct {
	description string
	tags        []string
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := descRe.FindSubmatch(body); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}

	if m := tagsRe.FindSubmatch(body); m != nil {
		for _, tm := range tagRe.FindAllSubmatch(m[1], -1) {
			tag := strings.TrimSpace(html.UnescapeString(string(tm[1])))
			if tag != "" {
				d.tags = append(d.tags, tag)
			}
		}
	}

	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:         ls.id,
		SiteID:     siteID,
		StudioURL:  s.base + "/categories/movies.html",
		Title:      ls.title,
		URL:        ls.url,
		Date:       ls.date,
		Thumbnail:  ls.thumb,
		Studio:     "Xev Bellringer",
		Performers: []string{"Xev Bellringer"},
		ScrapedAt:  now,
	}

	if ls.price > 0 {
		scene.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: ls.price,
		})
	}

	if ls.url != "" {
		body, err := s.fetchPage(ctx, ls.url)
		if err != nil {
			return models.Scene{}, fmt.Errorf("detail %s: %w", ls.id, err)
		}
		detail := parseDetailPage(body)
		scene.Description = detail.description
		scene.Tags = detail.tags
	}

	return scene, nil
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
