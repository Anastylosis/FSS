// Package woodmanfilms scrapes Woodman Films (woodmanfilms.com), covering the
// main /movie listing and the /pornstar/{slug}_{id} mode.
//
// Scene.Date is deliberately left zero: the site publishes no release date
// anywhere. Scene pages, movie pages and listing cards were all checked — the
// only four-digit years in the markup are image dimensions and the footer
// copyright. Its sibling wunf (same CMS family, same CDN) does carry
// "Publish Date : 7 August 2010" on scene pages, which makes the absence here
// look like a parser gap; it is not. ValidateScene logs the zero date as an
// advisory note, so live tests report it on every scene and pass.
package woodmanfilms

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
	siteID   = "woodmanfilms"
	siteBase = "https://www.woodmanfilms.com"
	pageSize = 20
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?woodmanfilms\.com(?:/|$)`)
	pornstarRe = regexp.MustCompile(`/pornstar/([\w-]+_\d+)`)
)

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		Client: httpx.NewClient(30 * time.Second),
		base:   siteBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"woodmanfilms.com/",
		"woodmanfilms.com/pornstar/{slug}_{id}",
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

	if m := pornstarRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping pornstar page %s", siteID, m[1])
		s.runPornstar(ctx, studioURL, opts, out)
		return
	}

	scraper.Debugf(1, "%s: scraping main listing", siteID)
	s.runListing(ctx, studioURL, opts, out)
}

func (s *Scraper) runListing(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listingScene)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ls := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				scene, err := s.fetchDetail(ctx, ls, studioURL)
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
		s.enqueuePages(ctx, studioURL, opts, out, work)
	}()

	wg.Wait()
}

func (s *Scraper) enqueuePages(ctx context.Context, _ string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
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
		scraper.Debugf(1, "%s: fetching page %d", siteID, page)

		u := fmt.Sprintf("%s/scene?page=%d", s.base, page)
		body, err := s.fetchPage(ctx, u)
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
			if maxPage > 0 {
				total := maxPage * len(scenes)
				scraper.Debugf(1, "%s: ~%d total scenes (%d pages)", siteID, total, maxPage)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
		}

		for _, ls := range scenes {
			if opts.KnownIDs != nil && opts.KnownIDs[ls.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, ls.id)
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

		if len(scenes) < pageSize {
			return
		}
	}
}

func (s *Scraper) runPornstar(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()

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

		u := studioURL
		if page > 1 {
			u = fmt.Sprintf("%s?page=%d", studioURL, page)
		}

		body, err := s.fetchPage(ctx, u)
		if err != nil {
			select {
			case out <- scraper.Error(err):
			case <-ctx.Done():
			}
			return
		}

		starName, scenes := parsePornstarPage(body, s.base)
		if page == 1 {
			scraper.Debugf(1, "%s: pornstar %s", siteID, starName)
		}

		if len(scenes) == 0 {
			return
		}

		// The page is already fetched and parsed, so a known scene mid-page is
		// no reason to discard the rest of it. Emit what is left, then stop
		// requesting further pages — that is where the early stop actually
		// saves something.
		hitKnown := false
		for _, ls := range scenes {
			if opts.KnownIDs != nil && opts.KnownIDs[ls.id] {
				hitKnown = true
				continue
			}

			scene := models.Scene{
				ID:         ls.id,
				SiteID:     siteID,
				StudioURL:  studioURL,
				Title:      ls.title,
				URL:        ls.url,
				Studio:     "Woodman Films",
				Thumbnail:  ls.thumb,
				Duration:   ls.duration,
				Performers: []string{starName},
				ScrapedAt:  now,
			}
			select {
			case out <- scraper.Scene(scene):
			case <-ctx.Done():
				return
			}
		}

		if hitKnown {
			scraper.Debugf(1, "%s: page %d held a stored scene, stopping after it", siteID, page)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}

		if len(scenes) < pageSize {
			return
		}
	}
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: defaultHeaders(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, studioURL string) (models.Scene, error) {
	body, err := s.fetchPage(ctx, ls.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", ls.id, err)
	}

	det := parseDetailPage(body)

	scene := models.Scene{
		ID:        ls.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     ls.title,
		URL:       ls.url,
		Studio:    "Woodman Films",
		Thumbnail: ls.thumb,
		ScrapedAt: time.Now().UTC(),
	}

	if det.title != "" {
		scene.Title = det.title
	}
	if det.description != "" {
		scene.Description = det.description
	}

	if len(det.performers) > 0 {
		scene.Performers = det.performers
	} else if len(ls.performers) > 0 {
		scene.Performers = ls.performers
	}

	if det.duration > 0 {
		scene.Duration = det.duration
	} else if ls.duration > 0 {
		scene.Duration = ls.duration
	}

	if ls.series != "" {
		scene.Series = ls.series
	}

	return scene, nil
}

func defaultHeaders() map[string]string {
	return httpx.BrowserHeaders(httpx.UserAgentFirefox)
}

// ---- listing page parsing ----

type listingScene struct {
	id  string
	url string
	// performers is the card's Casting line, which names the whole cast on one
	// line separated by commas — storing it verbatim filed a two-hander as a
	// single performer called "Jane Doe, John Smith".
	performers []string
	title      string
	series     string
	thumb      string
	duration   int
}

// splitCast turns the card's Casting line into individual names. The site
// writes the whole cast on one line, separated by commas.
func splitCast(raw string) []string {
	var names []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(html.UnescapeString(raw), ",") {
		name := titleCase(strings.TrimSpace(part))
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		names = append(names, name)
	}
	return names
}

var (
	blockStartRe = regexp.MustCompile(`<div class="block_960_item">`)
	hrefRe       = regexp.MustCompile(`<a href="(/scene/[^"]+)"`)
	itemTitRe    = regexp.MustCompile(`<div class="item_title">([^<]+)</div>`)
	itemImgRe    = regexp.MustCompile(`<img src="([^"]+)"`)
	infoH3Re     = regexp.MustCompile(`(?s)<h3>([^<]+)</h3>`)
	lengthRe     = regexp.MustCompile(`Length\s*:\s*([^<\n]+)`)
	castingRe    = regexp.MustCompile(`Casting\s*:\s*([^<\n]+)`)
	sceneIDRe    = regexp.MustCompile(`_(\d+)(?:\.html)?$`)
	lastPageRe   = regexp.MustCompile(`<a href="/scene\?page=(\d+)"[^>]*class="item last"`)
	pageNumRe    = regexp.MustCompile(`page=(\d+)`)
)

func parseListingPage(body []byte, base string) []listingScene {
	page := string(body)
	locs := blockStartRe.FindAllStringIndex(page, -1)
	var scenes []listingScene

	for i, loc := range locs {
		start := loc[0]
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[start:end]

		hm := hrefRe.FindStringSubmatch(block)
		if hm == nil {
			continue
		}
		href := hm[1]
		id := extractID(href)
		if id == "" {
			continue
		}

		ls := listingScene{
			id:  id,
			url: base + href,
		}

		if tm := itemTitRe.FindStringSubmatch(block); tm != nil {
			ls.series = strings.TrimSpace(html.UnescapeString(tm[1]))
		}

		if im := itemImgRe.FindStringSubmatch(block); im != nil {
			ls.thumb = im[1]
		}

		if h3 := infoH3Re.FindStringSubmatch(block); h3 != nil {
			ls.title = strings.TrimSpace(html.UnescapeString(h3[1]))
		}

		if lm := lengthRe.FindStringSubmatch(block); lm != nil {
			ls.duration = parseDurationText(strings.TrimSpace(lm[1]))
		}

		if cm := castingRe.FindStringSubmatch(block); cm != nil {
			ls.performers = splitCast(cm[1])
		}

		scenes = append(scenes, ls)
	}
	return scenes
}

func extractID(path string) string {
	clean := strings.TrimSuffix(path, ".html")
	if m := sceneIDRe.FindStringSubmatch(clean); m != nil {
		return m[1]
	}
	return ""
}

func parseMaxPage(body []byte) int {
	page := string(body)

	if m := lastPageRe.FindStringSubmatch(page); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}

	maxPage := 1
	for _, m := range pageNumRe.FindAllStringSubmatch(page, -1) {
		n, _ := strconv.Atoi(m[1])
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

// parseDurationText handles "14min", "2 Hour 10min", "1 Hour 15min", etc.
func parseDurationText(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	var total int

	hRe := regexp.MustCompile(`(\d+)\s*hour`)
	mRe := regexp.MustCompile(`(\d+)\s*min`)

	if m := hRe.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		total += h * 3600
	}
	if m := mRe.FindStringSubmatch(s); m != nil {
		mn, _ := strconv.Atoi(m[1])
		total += mn * 60
	}
	return total
}

// ---- detail page parsing ----

type detailData struct {
	title       string
	description string
	duration    int
	performers  []string
}

var (
	detTitleRe = regexp.MustCompile(`<span class="scene_title">([^<]+)</span>`)
	detDurRe   = regexp.MustCompile(`(?s)<span class="label_info">Length</span>\s*:\s*<span class="yellow">([^<]+)</span>`)
	detStarRe  = regexp.MustCompile(`(?s)<a href="/pornstar/[^"]*">\s*<h3>([^<]+)</h3>`)
	detMovieRe = regexp.MustCompile(`(?s)<div class="movie_infos">(.*?)</div>`)
	movieDescR = regexp.MustCompile(`(?s)<label>Description\s*:</label>\s*([^<]+)`)
)

func parseDetailPage(body []byte) detailData {
	var d detailData
	page := string(body)

	if m := detTitleRe.FindStringSubmatch(page); m != nil {
		d.title = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	if m := detDurRe.FindStringSubmatch(page); m != nil {
		d.duration = parseDurationText(strings.TrimSpace(m[1]))
	}

	for _, m := range detStarRe.FindAllStringSubmatch(page, -1) {
		name := strings.TrimSpace(html.UnescapeString(m[1]))
		if name != "" {
			d.performers = append(d.performers, titleCase(name))
		}
	}

	if block := detMovieRe.FindStringSubmatch(page); block != nil {
		if dm := movieDescR.FindStringSubmatch(block[1]); dm != nil {
			d.description = strings.TrimSpace(html.UnescapeString(dm[1]))
		}
	}

	return d
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}

// ---- pornstar page parsing ----

func parsePornstarPage(body []byte, base string) (string, []listingScene) {
	scenes := parseListingPage(body, base)

	var name string
	page := string(body)
	if m := regexp.MustCompile(`<h1>([^<]+)</h1>`).FindStringSubmatch(page); m != nil {
		name = titleCase(strings.TrimSpace(html.UnescapeString(m[1])))
	}

	return name, scenes
}
