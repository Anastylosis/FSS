package wunf

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID   = "wunf"
	siteBase = "https://www.wakeupnfuck.com"
	pageSize = 24
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?wakeupnfuck\.com(?:/|$)`)
	actorRe = regexp.MustCompile(`/actor/([\w-]+_\d+)`)
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
		"wakeupnfuck.com/",
		"wakeupnfuck.com/actor/{slug}_{id}",
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

	if m := actorRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping actor page %s", siteID, m[1])
		s.runActor(ctx, studioURL, opts, out)
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

		scenes, found := parseListingPage(body, s.base)
		if found == 0 {
			return
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

		if found < pageSize {
			return
		}
	}
}

func (s *Scraper) runActor(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetchPage(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	now := time.Now().UTC()
	actorName, scenes := parseActorPage(body, s.base)
	scraper.Debugf(1, "%s: actor %s has %d scenes", siteID, actorName, len(scenes))

	if len(scenes) > 0 {
		select {
		case out <- scraper.Progress(len(scenes)):
		case <-ctx.Done():
			return
		}
	}

	for _, ls := range scenes {
		if opts.KnownIDs != nil && opts.KnownIDs[ls.id] {
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}

		scene := models.Scene{
			ID:         ls.id,
			SiteID:     siteID,
			StudioURL:  studioURL,
			Title:      ls.title,
			URL:        ls.url,
			Studio:     "Wake Up n Fuck",
			Thumbnail:  ls.thumb,
			Duration:   ls.duration,
			Performers: []string{actorName},
			ScrapedAt:  now,
		}
		if !ls.date.IsZero() {
			scene.Date = ls.date
		}
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
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
		Studio:    "Wake Up n Fuck",
		Thumbnail: ls.thumb,
		ScrapedAt: time.Now().UTC(),
	}

	if det.title != "" {
		scene.Title = det.title
	}
	scene.Tags = det.tags

	if len(det.performers) > 0 {
		scene.Performers = det.performers
	} else if ls.performer != "" {
		scene.Performers = []string{ls.performer}
	}

	if !det.date.IsZero() {
		scene.Date = det.date
	}

	if det.duration > 0 {
		scene.Duration = det.duration
	} else if ls.duration > 0 {
		scene.Duration = ls.duration
	}

	return scene, nil
}

func defaultHeaders() map[string]string {
	return httpx.BrowserHeaders(httpx.UserAgentFirefox)
}

// ---- listing page parsing ----

type listingScene struct {
	id        string
	url       string
	title     string
	performer string
	thumb     string
	duration  int
	date      time.Time
}

var (
	sceneEntryRe = regexp.MustCompile(`(?s)<a href="(/scene/[^"]+)" class="scene item[^"]*"[^>]*>(.*?)</a>`)
	// The closing tag is deliberately not required. Scene cards on the main
	// listing close their <h3>; cards on an actor page do not — they run
	// straight into the next <p> ("<h3>Dido Angel - WUNF 3    <p>0/5</p>").
	// Demanding </h3> matched nothing there, so every scene scraped from an
	// actor page came back with an empty Title. `[^<]+` stops at the next tag
	// either way, so this parses both templates identically.
	titleH3Re = regexp.MustCompile(`(?s)<h3>([^<]+)`)
	subRe     = regexp.MustCompile(`<p class="sub">([^<]+)</p>`)
	timerRe   = regexp.MustCompile(`<p class="timer">([^<]+)</p>`)
	imgRe     = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	sceneIDRe = regexp.MustCompile(`_(\d+)$`)
)

// parseListingPage returns the scenes on a listing page and the number of
// cards the page actually held. The two differ when a card's href yields no
// id, and only the raw count may be compared against pageSize: measuring the
// filtered slice made one unparseable card read as "last page" and hand
// --full's authoritative Save a truncated catalogue.
func parseListingPage(body []byte, base string) (scenes []listingScene, found int) {
	page := string(body)

	for _, m := range sceneEntryRe.FindAllStringSubmatch(page, -1) {
		found++
		href := m[1]
		block := m[2]

		id := extractID(href)
		if id == "" {
			continue
		}

		ls := listingScene{
			id:  id,
			url: base + href,
		}

		if tm := titleH3Re.FindStringSubmatch(block); tm != nil {
			ls.title = strings.TrimSpace(html.UnescapeString(tm[1]))
		}

		if sm := subRe.FindStringSubmatch(block); sm != nil {
			ls.performer = strings.TrimSpace(html.UnescapeString(sm[1]))
		}

		if dm := timerRe.FindStringSubmatch(block); dm != nil {
			ls.duration = parseutil.ParseDurationColon(strings.TrimSpace(dm[1]))
		}

		if im := imgRe.FindStringSubmatch(block); im != nil {
			ls.thumb = im[1]
		}

		scenes = append(scenes, ls)
	}
	return scenes, found
}

func extractID(path string) string {
	clean := strings.TrimSuffix(path, ".html")
	if m := sceneIDRe.FindStringSubmatch(clean); m != nil {
		return m[1]
	}
	return ""
}

// ---- detail page parsing ----

type detailData struct {
	title      string
	date       time.Time
	duration   int
	tags       []string
	performers []string
}

var (
	detTitleRe = regexp.MustCompile(`(?s)<h2>([^<]+)</h2>`)
	detDescRe  = regexp.MustCompile(`(?s)<div class="description">(.*?)</div>`)
	detDateRe  = regexp.MustCompile(`Publish Date\s*:\s*(\d{1,2}\s+\w+\s+\d{4})`)
	detLenRe   = regexp.MustCompile(`Length\s*:\s*([\d:]+)`)
	detTagRe   = regexp.MustCompile(`<li><a href="/tag/[^"]*">([^<]+)</a></li>`)
	detActorRe = regexp.MustCompile(`(?s)<div class="starring">.*?</div>`)
	actorNameR = regexp.MustCompile(`(?s)<div class="informations">\s*<p>([^<]+)</p>`)
)

func parseDetailPage(body []byte) detailData {
	var d detailData
	page := string(body)

	if m := detTitleRe.FindStringSubmatch(page); m != nil {
		d.title = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	if m := detDescRe.FindStringSubmatch(page); m != nil {
		desc := m[1]
		if dm := detDateRe.FindStringSubmatch(desc); dm != nil {
			if t, err := time.Parse("2 January 2006", strings.TrimSpace(dm[1])); err == nil {
				d.date = t.UTC()
			}
		}
		if lm := detLenRe.FindStringSubmatch(desc); lm != nil {
			d.duration = parseutil.ParseDurationColon(strings.TrimSpace(lm[1]))
		}
	}

	for _, m := range detTagRe.FindAllStringSubmatch(page, -1) {
		tag := strings.TrimSpace(html.UnescapeString(m[1]))
		if tag != "" {
			d.tags = append(d.tags, tag)
		}
	}

	if block := detActorRe.FindString(page); block != "" {
		for _, m := range actorNameR.FindAllStringSubmatch(block, -1) {
			name := strings.TrimSpace(html.UnescapeString(m[1]))
			if name != "" {
				d.performers = append(d.performers, titleCase(name))
			}
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

// ---- actor page parsing ----

var (
	actorNamePageRe = regexp.MustCompile(`<h1>([^<]+)</h1>`)
	actorSceneRe    = regexp.MustCompile(`(?s)<a href="(/scene/[^"]+)" class="scene item[^"]*">(.*?)</a>`)
	actorDateRe     = regexp.MustCompile(`(\d{4}-\d{2}-\d{2})\s*\d{2}:\d{2}:\d{2}`)
)

func parseActorPage(body []byte, base string) (string, []listingScene) {
	page := string(body)

	var actorName string
	if m := actorNamePageRe.FindStringSubmatch(page); m != nil {
		actorName = titleCase(strings.TrimSpace(html.UnescapeString(m[1])))
	}

	var scenes []listingScene
	for _, m := range actorSceneRe.FindAllStringSubmatch(page, -1) {
		href := m[1]
		block := m[2]

		// Skip cross-site links to woodmancastingx.com
		if strings.Contains(href, "woodmancastingx") {
			continue
		}

		id := extractID(href)
		if id == "" {
			continue
		}

		ls := listingScene{
			id:  id,
			url: base + href,
		}

		if tm := titleH3Re.FindStringSubmatch(block); tm != nil {
			ls.title = strings.TrimSpace(html.UnescapeString(tm[1]))
		}

		if dm := timerRe.FindStringSubmatch(block); dm != nil {
			ls.duration = parseutil.ParseDurationColon(strings.TrimSpace(dm[1]))
		}

		if im := imgRe.FindStringSubmatch(block); im != nil {
			ls.thumb = im[1]
		}

		if dateM := actorDateRe.FindStringSubmatch(block); dateM != nil {
			if t, err := time.Parse("2006-01-02", dateM[1]); err == nil {
				ls.date = t.UTC()
			}
		}

		scenes = append(scenes, ls)
	}
	return actorName, scenes
}
