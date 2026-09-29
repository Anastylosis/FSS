package alisonangel

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

const siteBase = "https://alisonangel.com"

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "alisonangel" }

func (s *Scraper) Patterns() []string {
	return []string{
		"alisonangel.com",
		"alisonangel.com/episode/{slug}-{id}.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?alisonangel\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type episode struct {
	id   string
	path string
}

type listingEntry struct {
	id    string
	title string
	date  time.Time
	dur   int
	thumb string
}

var (
	epboxRe    = regexp.MustCompile(`(?s)<div class="epbox">(.*?)</div>\s*<div class="clear">`)
	eplistRe   = regexp.MustCompile(`<div class="eplistcell"><a href="(/episode/[^"]+)"`)
	epTitleRe  = regexp.MustCompile(`<div class="eptitle"><a href="[^"]*">([^<]+)</a></div>`)
	epDateRe   = regexp.MustCompile(`<strong>Release Date:</strong>\s*(\d{4}-\d{2}-\d{2})`)
	epDurRe    = regexp.MustCompile(`<strong>Video Length:</strong>\s*(\d+):(\d+)\s*mins?`)
	epThumbRe  = regexp.MustCompile(`<div class="episodepic2"><a href="[^"]*"><img src="([^"]*)"`)
	epLinkRe   = regexp.MustCompile(`<a href="(/episode/[^"]+)"`)
	slugIDRe   = regexp.MustCompile(`-(\d+)\.html$`)
	detTitleRe = regexp.MustCompile(`<div class="episodetitle"><a[^>]*>([^<]+)</a></div>`)
	detDurRe   = regexp.MustCompile(`<div class="ep_vids"><strong>(\d+):(\d+)\s*mins?</strong>`)
	detDescRe  = regexp.MustCompile(`(?s)<div class="epdetails">(.*?)</div>`)
	detThumbRe = regexp.MustCompile(`<div class="episodepic"><a[^>]*><img src="([^"]*)"`)
	continueRe = regexp.MustCompile(`<a href="(/episode/[^"]*)"[^>]*><img src="/images/bt_continue1\.jpg"`)
)

func parseSlugID(href string) string {
	if m := slugIDRe.FindStringSubmatch(href); m != nil {
		return m[1]
	}
	return ""
}

func parseHomepage(body []byte) (featured []listingEntry, episodes []episode) {
	seen := make(map[string]bool)

	for _, m := range epboxRe.FindAllSubmatch(body, -1) {
		block := string(m[1])
		var e listingEntry

		links := epLinkRe.FindAllStringSubmatch(block, -1)
		if len(links) == 0 {
			continue
		}
		path := links[0][1]
		e.id = parseSlugID(path)
		if e.id == "" {
			continue
		}

		if tm := epTitleRe.FindStringSubmatch(block); tm != nil {
			e.title = strings.TrimSpace(html.UnescapeString(tm[1]))
		}
		if dm := epDateRe.FindStringSubmatch(block); dm != nil {
			if t, err := time.Parse("2006-01-02", dm[1]); err == nil {
				e.date = t.UTC()
			}
		}
		if dur := epDurRe.FindStringSubmatch(block); dur != nil {
			mins, _ := strconv.Atoi(dur[1])
			secs, _ := strconv.Atoi(dur[2])
			e.dur = mins*60 + secs
		}
		if th := epThumbRe.FindStringSubmatch(block); th != nil {
			e.thumb = th[1]
		}

		featured = append(featured, e)
		if !seen[e.id] {
			seen[e.id] = true
			episodes = append(episodes, episode{id: e.id, path: path})
		}
	}

	for _, m := range eplistRe.FindAllSubmatch(body, -1) {
		path := string(m[1])
		id := parseSlugID(path)
		if id != "" && !seen[id] {
			seen[id] = true
			episodes = append(episodes, episode{id: id, path: path})
		}
	}

	return featured, episodes
}

type detailPage struct {
	title    string
	dur      int
	desc     string
	thumb    string
	nextPath string
}

func parseDetailPage(body []byte) detailPage {
	var d detailPage

	if m := detTitleRe.FindSubmatch(body); m != nil {
		d.title = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}
	if m := detDurRe.FindSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(string(m[1]))
		secs, _ := strconv.Atoi(string(m[2]))
		d.dur = mins*60 + secs
	}
	if m := detDescRe.FindSubmatch(body); m != nil {
		d.desc = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}
	if m := detThumbRe.FindSubmatch(body); m != nil {
		d.thumb = string(m[1])
	}
	if m := continueRe.FindSubmatch(body); m != nil {
		d.nextPath = string(m[1])
	}

	return d
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = 3
	}

	body, err := s.fetch(ctx, s.base+"/")
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("homepage: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	featured, homepageEps := parseHomepage(body)
	if len(homepageEps) == 0 {
		select {
		case out <- scraper.Error(fmt.Errorf("no episodes found on homepage")):
		case <-ctx.Done():
		}
		return
	}

	enrichment := make(map[string]listingEntry, len(featured))
	for _, e := range featured {
		enrichment[e.id] = e
	}

	allEps := s.discoverChain(ctx, homepageEps, opts)

	select {
	case out <- scraper.Progress(len(allEps)):
	case <-ctx.Done():
		return
	}

	work := make(chan episode, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ep := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, ferr := s.fetchScene(ctx, ep, studioURL, enrichment)
				if ferr != nil {
					select {
					case out <- scraper.Error(ferr):
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

	cancelled := false
	for _, ep := range allEps {
		if opts.KnownIDs[ep.id] {
			scraper.Debugf(1, "alisonangel: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			break
		}
		select {
		case work <- ep:
		case <-ctx.Done():
			cancelled = true
		}
		if cancelled {
			break
		}
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) discoverChain(ctx context.Context, homepageEps []episode, opts scraper.ListOpts) []episode {
	if len(homepageEps) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(homepageEps))
	all := make([]episode, 0, len(homepageEps)*2)
	for _, ep := range homepageEps {
		seen[ep.id] = true
		all = append(all, ep)
	}

	lastPath := homepageEps[len(homepageEps)-1].path

	for ctx.Err() == nil {
		if !scraper.Pace(ctx, opts.Delay) {
			return all
		}

		body, err := s.fetch(ctx, s.base+lastPath)
		if err != nil || len(body) == 0 {
			break
		}

		d := parseDetailPage(body)
		if d.nextPath == "" {
			break
		}

		nextID := parseSlugID(d.nextPath)
		if nextID == "" || seen[nextID] {
			break
		}

		seen[nextID] = true
		all = append(all, episode{id: nextID, path: d.nextPath})
		lastPath = d.nextPath
	}

	return all
}

func (s *Scraper) fetchScene(ctx context.Context, ep episode, studioURL string, enrichment map[string]listingEntry) (models.Scene, error) {
	url := s.base + ep.path

	body, err := s.fetch(ctx, url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("episode %s: %w", ep.id, err)
	}
	if len(body) == 0 {
		return models.Scene{}, fmt.Errorf("episode %s: empty page", ep.id)
	}

	d := parseDetailPage(body)
	if d.title == "" {
		return models.Scene{}, fmt.Errorf("episode %s: no title", ep.id)
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:          ep.id,
		SiteID:      "alisonangel",
		StudioURL:   studioURL,
		URL:         url,
		Title:       d.title,
		Description: d.desc,
		Thumbnail:   d.thumb,
		Duration:    d.dur,
		Performers:  []string{"Alison Angel"},
		Studio:      "Alison Angel",
		ScrapedAt:   now,
	}

	if le, ok := enrichment[ep.id]; ok {
		scene.Date = le.date
		if scene.Duration == 0 {
			scene.Duration = le.dur
		}
		if scene.Thumbnail == "" {
			scene.Thumbnail = le.thumb
		}
	}

	return scene, nil
}

func (s *Scraper) fetch(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
