package xespl

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
	defaultBase = "https://xes.pl"
	siteID      = "xespl"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?xes\.pl(?:/|$)`)

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
	return []string{
		"xes.pl",
		"xes.pl/katalog_filmow,{page}.html",
		"xes.pl/aktor,{slug},{id},{page}.html",
		"xes.pl/produkcja,{slug},{page}.html",
		"xes.pl/filtr,{category},{page}.html",
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

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	templateURL := normalizeURL(s.base, studioURL)

	work := make(chan listingScene)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ls := range work {
				scene, err := s.fetchDetail(ctx, ls, studioURL, opts.Delay)
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)
		s.enqueuePages(ctx, templateURL, opts, out, work)
	}()

	wg.Wait()
}

func normalizeURL(base, studioURL string) string {
	u := strings.TrimRight(studioURL, "/")
	if u == base || u == "https://www.xes.pl" || !strings.Contains(u, ",") {
		return base + "/katalog_filmow,1.html"
	}
	return studioURL
}

func buildPageURL(templateURL string, page int) string {
	idx := strings.LastIndex(templateURL, ",")
	if idx < 0 {
		return templateURL
	}
	return templateURL[:idx] + fmt.Sprintf(",%d.html", page)
}

func (s *Scraper) enqueuePages(ctx context.Context, templateURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return
			}
		}
		scraper.Debugf(1, "xespl: fetching page %d", page)

		pageURL := buildPageURL(templateURL, page)
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
			maxPage := parseMaxPage(body)
			total := maxPage * len(scenes)
			if total > 0 {
				scraper.Debugf(1, "xespl: %d total scenes", total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
		}

		for _, ls := range scenes {
			if opts.KnownIDs[ls.id] {
				scraper.Debugf(1, "xespl: hit known ID, stopping early")
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
	thumb string
}

var (
	cardStartRe  = regexp.MustCompile(`class="videoCatalogCard"`)
	epLinkRe     = regexp.MustCompile(`href="(epizod,(\d+),[^"]+\.html)"`)
	listTitleRe  = regexp.MustCompile(`(?s)<h2><a[^>]*>(.*?)</a></h2>`)
	listThumbRe  = regexp.MustCompile(`<img src="([^"]+/slider(?:_org)?\.jpg)"`)
	paginationRe = regexp.MustCompile(`(?s)<ul class="pagination">(.*?)</ul>`)
	pageNumRe    = regexp.MustCompile(`>(\d+)</a>`)
)

func parseListingPage(body []byte, base string) []listingScene {
	page := string(body)
	locs := cardStartRe.FindAllStringIndex(page, -1)
	scenes := make([]listingScene, 0, len(locs))

	for i, loc := range locs {
		start := loc[0]
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[start:end]

		var ls listingScene

		if m := epLinkRe.FindStringSubmatch(block); m != nil {
			href := m[1]
			if !strings.HasPrefix(href, "http") {
				href = base + "/" + href
			}
			ls.url = href
			ls.id = m[2]
		}

		if m := listTitleRe.FindStringSubmatch(block); m != nil {
			ls.title = strings.TrimSpace(html.UnescapeString(m[1]))
		}

		if m := listThumbRe.FindStringSubmatch(block); m != nil {
			thumb := m[1]
			if strings.HasPrefix(thumb, "/") {
				thumb = base + thumb
			}
			ls.thumb = thumb
		}

		if ls.url == "" || ls.id == "" {
			continue
		}

		scenes = append(scenes, ls)
	}
	return scenes
}

func parseMaxPage(body []byte) int {
	m := paginationRe.FindSubmatch(body)
	if m == nil {
		return 1
	}
	maxPage := 1
	for _, pm := range pageNumRe.FindAllSubmatch(m[1], -1) {
		n, _ := strconv.Atoi(string(pm[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

var (
	detTitleRe   = regexp.MustCompile(`(?s)<h1 class="arrow">(.*?)</h1>`)
	titleSpanRe  = regexp.MustCompile(`(?s)<span>.*?</span>`)
	descRe       = regexp.MustCompile(`(?s)<p class="padding10">(.*?)</p>`)
	durationRe   = regexp.MustCompile(`Duration:</td>\s*<td>(\d{2}):(\d{2}):(\d{2})</td>`)
	dateRe       = regexp.MustCompile(`Add date:</td>\s*<td>(\d{4}-\d{2}-\d{2})</td>`)
	producerRe   = regexp.MustCompile(`class="producerLink"[^>]*>([^<]+)</a>`)
	catBlockRe   = regexp.MustCompile(`(?s)Categories:</td>\s*<td>(.*?)</td>`)
	actorBlockRe = regexp.MustCompile(`(?s)Actors:</td>\s*<td>(.*?)</td>`)
	linkTextRe   = regexp.MustCompile(`>([^<]+)</a>`)
	priceRe      = regexp.MustCompile(`class="price">(\d+)\s*pts</span>`)
	viewsRe      = regexp.MustCompile(`Views:</td>\s*<td>(\d+)</td>`)
	resolutionRe = regexp.MustCompile(`Resolution:</td>\s*<td>(\d+x\d+)</td>`)
)

type detailData struct {
	title       string
	description string
	date        time.Time
	duration    int
	producer    string
	categories  []string
	performers  []string
	resolution  string
	views       int
	price       int
}

func parseDetailPage(body []byte) detailData {
	var d detailData
	page := string(body)

	if m := detTitleRe.FindStringSubmatch(page); m != nil {
		raw := titleSpanRe.ReplaceAllString(m[1], "")
		d.title = strings.TrimSpace(html.UnescapeString(raw))
	}

	if m := descRe.FindStringSubmatch(page); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	if m := durationRe.FindStringSubmatch(page); m != nil {
		h, _ := strconv.Atoi(m[1])
		mins, _ := strconv.Atoi(m[2])
		sec, _ := strconv.Atoi(m[3])
		d.duration = h*3600 + mins*60 + sec
	}

	if m := dateRe.FindStringSubmatch(page); m != nil {
		if t, err := time.Parse("2006-01-02", m[1]); err == nil {
			d.date = t.UTC()
		}
	}

	if m := producerRe.FindStringSubmatch(page); m != nil {
		d.producer = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	if m := catBlockRe.FindStringSubmatch(page); m != nil {
		for _, cm := range linkTextRe.FindAllStringSubmatch(m[1], -1) {
			cat := strings.TrimSpace(html.UnescapeString(cm[1]))
			if cat != "" {
				d.categories = append(d.categories, cat)
			}
		}
	}

	if m := actorBlockRe.FindStringSubmatch(page); m != nil {
		for _, am := range linkTextRe.FindAllStringSubmatch(m[1], -1) {
			name := strings.TrimSpace(html.UnescapeString(am[1]))
			if name != "" {
				d.performers = append(d.performers, name)
			}
		}
	}

	if m := priceRe.FindStringSubmatch(page); m != nil {
		d.price, _ = strconv.Atoi(m[1])
	}

	if m := viewsRe.FindStringSubmatch(page); m != nil {
		d.views, _ = strconv.Atoi(m[1])
	}

	if m := resolutionRe.FindStringSubmatch(page); m != nil {
		d.resolution = m[1]
	}

	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, studioURL string, delay time.Duration) (models.Scene, error) {
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return models.Scene{}, ctx.Err()
		}
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:        ls.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       ls.url,
		Title:     ls.title,
		Thumbnail: ls.thumb,
		Studio:    "Xes.pl",
		ScrapedAt: now,
	}

	body, err := s.fetchPage(ctx, ls.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", ls.id, err)
	}

	detail := parseDetailPage(body)
	if detail.title != "" {
		scene.Title = detail.title
	}
	scene.Description = detail.description
	scene.Date = detail.date
	scene.Duration = detail.duration
	scene.Resolution = detail.resolution
	scene.Views = detail.views
	scene.Tags = detail.categories
	if len(detail.performers) > 0 {
		scene.Performers = detail.performers
	}
	if detail.producer != "" {
		scene.Series = detail.producer
	}
	if detail.price > 0 {
		scene.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: float64(detail.price),
		})
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
