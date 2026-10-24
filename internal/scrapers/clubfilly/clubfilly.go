// Package clubfilly scrapes Club Filly (clubfilly.com), the membership site of
// Filly Films. The scene catalogue is enumerated from /sitemap.html, the one
// page whose scene links agree with the detail pages: the paginated
// /scenes-N.html listing and the performer/DVD pages pair titles with the
// wrong scene IDs on older entries. Each /scenefocus-V{id}.html detail page
// carries title, release date, description and performers.
package clubfilly

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
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
	siteID        = "clubfilly"
	studioName    = "Club Filly"
	detailWorkers = 4
)

// siteBase is a var so tests can point it at an httptest server.
var siteBase = "https://clubfilly.com"

type Scraper struct {
	Client *http.Client
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"clubfilly.com",
		"clubfilly.com/home.php",
		"clubfilly.com/home.html",
		"clubfilly.com/scenes.html",
	}
}

// Only the whole catalogue is supported: the site's performer and DVD pages
// link to the wrong scenes, so they are deliberately not matched.
var matchRe = regexp.MustCompile(`^https?://(?:www\.)?clubfilly\.com(?:/(?:home\.(?:php|html)|scenes(?:-\d+)?\.html|index\.(?:php|html)|sitemap\.html)?)?/?(?:[?#].*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	sitemapSceneRe = regexp.MustCompile(`href="/scenefocus-V(\d+)\.html">Club Filly - Video - `)
	titleRe        = regexp.MustCompile(`(?s)<div class="boxVidDetail">.*?<h1><span>(.*?)</span></h1>`)
	dateRe         = regexp.MustCompile(`Release Date\s*:\s*(\d{4}-\d{2}-\d{2})`)
	descRe         = regexp.MustCompile(`(?s)<p class="description"><span>Description:</span>(.*?)</p>`)
	starringRe     = regexp.MustCompile(`(?s)<p class="starring"><span>Starring:</span>(.*?)</p>`)
	focusImgRe     = regexp.MustCompile(`(?s)<ul id="lstSceneFocus">(.*?)</ul>`)
	imgSrcRe       = regexp.MustCompile(`<img src="([^"]+)"`)
	tagRe          = regexp.MustCompile(`<[^>]*>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	send := func(r scraper.SceneResult) bool {
		select {
		case out <- r:
			return true
		case <-ctx.Done():
			return false
		}
	}

	ids, err := s.fetchIDs(ctx)
	if err != nil {
		send(scraper.Error(err))
		return
	}

	// Newest scenes carry the highest IDs. Known scenes are skipped without a
	// detail request; no walk order to preserve, so nothing older is missed.
	var todo []string
	skipped := 0
	for _, id := range ids {
		if opts.KnownIDs[id] {
			skipped++
			continue
		}
		todo = append(todo, id)
	}
	scraper.Debugf(1, "%s: %d scenes in sitemap, %d known, %d to fetch", siteID, len(ids), skipped, len(todo))
	if !send(scraper.Progress(len(todo))) {
		return
	}

	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(todo), workers)

	now := time.Now().UTC()
	jobs := make(chan string)
	results := make(chan scraper.SceneResult)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				var r scraper.SceneResult
				if sc, err := s.fetchDetail(ctx, studioURL, id, now); err != nil {
					r = scraper.Error(err)
				} else {
					r = scraper.Scene(sc)
				}
				select {
				case results <- r:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, id := range todo {
			select {
			case jobs <- id:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		if !send(r) {
			for range results {
			}
			return
		}
	}
	if skipped > 0 {
		send(scraper.StoppedEarly())
	}
}

func (s *Scraper) fetchIDs(ctx context.Context) ([]string, error) {
	u := siteBase + "/sitemap.html"
	body, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ids []string
	for _, m := range sitemapSceneRe.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	if len(ids) == 0 {
		return nil, scraper.ParseError(u, fmt.Errorf("no scene links in sitemap"))
	}
	sort.SliceStable(ids, func(i, j int) bool {
		a, _ := strconv.Atoi(ids[i])
		b, _ := strconv.Atoi(ids[j])
		return a > b
	})
	return ids, nil
}

func (s *Scraper) fetchDetail(ctx context.Context, studioURL, id string, now time.Time) (models.Scene, error) {
	u := fmt.Sprintf("%s/scenefocus-V%s.html", siteBase, id)
	body, err := s.get(ctx, u)
	if err != nil {
		return models.Scene{}, err
	}
	return parseDetail(body, studioURL, id, u, now)
}

func parseDetail(body, studioURL, id, pageURL string, now time.Time) (models.Scene, error) {
	m := titleRe.FindStringSubmatch(body)
	if m == nil {
		return models.Scene{}, scraper.ParseError(pageURL, fmt.Errorf("no scene title"))
	}
	sc := models.Scene{
		ID:        id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     clean(m[1]),
		URL:       pageURL,
		ScrapedAt: now,
	}
	if m := dateRe.FindStringSubmatch(body); m != nil {
		if d, err := parseutil.TryParseDate(m[1], "2006-01-02"); err == nil {
			sc.Date = d
		}
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(m[1])
	}
	if m := starringRe.FindStringSubmatch(body); m != nil {
		for _, p := range strings.Split(clean(m[1]), ",") {
			if p = strings.TrimSpace(p); p != "" {
				sc.Performers = append(sc.Performers, p)
			}
		}
	}
	// The gallery sometimes shows stills of other scenes; keep only an image
	// that belongs to this one.
	if m := focusImgRe.FindStringSubmatch(body); m != nil {
		own := "/images/V" + id + "/"
		for _, im := range imgSrcRe.FindAllStringSubmatch(m[1], -1) {
			if strings.HasPrefix(im[1], own) {
				sc.Thumbnail = siteBase + im[1]
				break
			}
		}
	}
	return sc, nil
}

func clean(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

func (s *Scraper) get(ctx context.Context, u string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
