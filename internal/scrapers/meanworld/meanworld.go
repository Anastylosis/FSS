// Package meanworld scrapes the Mean World megasite (megasite.meanworld.com),
// an ElevatedX VOD store that aggregates the whole Mean World tree — Mean
// Bitches, Slave Orders, Mean World Classic and the other channels. The
// standalone meanbitches.com domain is a dead static splash, so all content is
// reached through the megasite. Each `latestUpdateB` listing card carries the
// scene URL, title, performers, thumbnail, price and the channel section; the
// per-scene /scenes/{slug}_vids.html detail page adds the date, runtime and
// description.
//
// A channel-filtered scrape is supported by pointing the studio URL at a
// channel sub-path, e.g. https://megasite.meanworld.com/slaveorders/categories/movies_1_d.html
package meanworld

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
	siteID     = "meanworld"
	studioName = "Mean World"
	siteHost   = "https://megasite.meanworld.com"
	perPage    = 24
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?(?:megasite\.)?meanworld\.com(?:/|$)`)
	listBaseRe = regexp.MustCompile(`^(https?://megasite\.meanworld\.com(?:/[a-z0-9]+)?)/categories/`)
)

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"megasite.meanworld.com",
		"megasite.meanworld.com/categories/movies.html",
		"megasite.meanworld.com/{channel}/categories/movies.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// listBase resolves the listing prefix from the studio URL, honouring a
// channel sub-path when present; defaults to the megasite root.
func listBase(studioURL string) string {
	if m := listBaseRe.FindStringSubmatch(studioURL); m != nil {
		return m[1]
	}
	return siteHost
}

type listingScene struct {
	slug       string
	url        string
	title      string
	performers []string
	thumb      string
	section    string
	price      float64
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := listBase(studioURL)
	scraper.Debugf(1, "meanworld: using listing base %s", base)

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
		s.enqueueListing(ctx, base, opts, out, work)
	}()

	wg.Wait()
}

func (s *Scraper) enqueueListing(ctx context.Context, base string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "meanworld: fetching page %d", page)
		pageURL := fmt.Sprintf("%s/categories/movies_%d_d.html", base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}
		scenes := parseListing(body)
		if len(scenes) == 0 {
			return
		}
		if page == 1 {
			if total := estimateTotal(body); total > 0 {
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
		}
		for _, ls := range scenes {
			if opts.KnownIDs[ls.slug] {
				scraper.Debugf(1, "meanworld: hit known ID %s, stopping early", ls.slug)
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

var (
	cardRe    = regexp.MustCompile(`class="latestUpdateB" data-setid="(\d+)"`)
	sceneRe   = regexp.MustCompile(`href="((?:https?://[^"]*)?/?scenes/([A-Za-z0-9_-]+)_vids\.html)"`)
	titleRe   = regexp.MustCompile(`(?s)href="[^"]*scenes/[A-Za-z0-9_-]+_vids\.html"[^>]*>\s*([^<]*?)\s*</a>`)
	modelRe   = regexp.MustCompile(`href="[^"]*/models/[^"]*"[^>]*>([^<]+)</a>`)
	posterRe  = regexp.MustCompile(`poster_1x="([^"]+)"`)
	priceRe   = regexp.MustCompile(`Buy \(\$([0-9.]+)\)`)
	sectionRe = regexp.MustCompile(`"InternalLabel":"([^"]*?) Section`)
	pageOfRe  = regexp.MustCompile(`Page \d+ of (\d+)`)

	videoInfoRe  = regexp.MustCompile(`(?s)class="videoInfo"[^>]*>(.*?)</ul>`)
	detailDateRe = regexp.MustCompile(`(\d{2}/\d{2}/\d{4})`)
	detailMinRe  = regexp.MustCompile(`(\d+)\s*min`)
	metaDescRe   = regexp.MustCompile(`name="description" content="([^"]*)"`)
)

func parseListing(body []byte) []listingScene {
	page := string(body)
	locs := cardRe.FindAllStringSubmatchIndex(page, -1)
	scenes := make([]listingScene, 0, len(locs))
	seen := map[string]bool{}

	for i, loc := range locs {
		start := loc[0]
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[start:end]

		sm := sceneRe.FindStringSubmatch(block)
		if sm == nil {
			continue
		}
		slug := sm[2]
		if seen[slug] {
			continue
		}
		seen[slug] = true

		ls := listingScene{slug: slug, url: absURL(sm[1])}
		if m := titleRe.FindStringSubmatch(block); m != nil {
			ls.title = strings.TrimSpace(html.UnescapeString(m[1]))
		}
		for _, m := range modelRe.FindAllStringSubmatch(block, -1) {
			name := strings.TrimSpace(html.UnescapeString(m[1]))
			if name != "" {
				ls.performers = append(ls.performers, name)
			}
		}
		if m := posterRe.FindStringSubmatch(block); m != nil {
			ls.thumb = absURL(m[1])
		}
		if m := sectionRe.FindStringSubmatch(block); m != nil {
			ls.section = strings.TrimSpace(html.UnescapeString(m[1]))
		}
		if m := priceRe.FindStringSubmatch(block); m != nil {
			ls.price, _ = strconv.ParseFloat(m[1], 64)
		}
		if ls.title == "" {
			ls.title = slugToTitle(slug)
		}
		scenes = append(scenes, ls)
	}
	return scenes
}

func estimateTotal(body []byte) int {
	if m := pageOfRe.FindSubmatch(body); m != nil {
		if n, _ := strconv.Atoi(string(m[1])); n > 0 {
			return n * perPage
		}
	}
	return 0
}

type detailData struct {
	date        time.Time
	duration    int
	description string
}

// parseDetail reads the date, runtime and description from a /scenes/..._vids.html page.
func parseDetail(body []byte) detailData {
	var d detailData
	page := string(body)

	// The videoInfo block holds "MM/DD/YYYY <photos> <N> min".
	if m := videoInfoRe.FindStringSubmatch(page); m != nil {
		seg := m[1]
		if dm := detailDateRe.FindStringSubmatch(seg); dm != nil {
			if t, err := time.Parse("01/02/2006", dm[1]); err == nil {
				d.date = t.UTC()
			}
		}
		if mm := detailMinRe.FindStringSubmatch(seg); mm != nil {
			mins, _ := strconv.Atoi(mm[1])
			d.duration = mins * 60
		}
	}
	if m := metaDescRe.FindStringSubmatch(page); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:         ls.slug,
		SiteID:     siteID,
		StudioURL:  studioURL,
		URL:        ls.url,
		Title:      ls.title,
		Performers: ls.performers,
		Thumbnail:  ls.thumb,
		Studio:     studioName,
		Series:     ls.section,
		ScrapedAt:  now,
	}
	if ls.price > 0 {
		scene.AddPrice(models.PriceSnapshot{Date: now, Regular: ls.price})
	}

	body, err := s.fetchPage(ctx, ls.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", ls.slug, err)
	}
	d := parseDetail(body)
	scene.Date = d.date
	scene.Duration = d.duration
	scene.Description = d.description
	return scene, nil
}

func slugToTitle(slug string) string { return strings.ReplaceAll(slug, "-", " ") }

func absURL(u string) string {
	if strings.HasPrefix(u, "http") {
		return u
	}
	if strings.HasPrefix(u, "/") {
		return siteHost + u
	}
	return siteHost + "/" + u
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
