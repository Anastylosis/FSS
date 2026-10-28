// Package riggsfilms scrapes riggsfilms.com. Since its 2026-10 rebuild the
// scenes are a "vm_scene" post type with no REST route; the Yoast video
// sitemap lists every one with its metadata, and each scene page names its
// cast. See docs/scrapers.md.
package riggsfilms

import (
	"context"
	"errors"
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
	siteID        = "riggsfilms"
	studioName    = "Riggs Films"
	detailWorkers = 4
)

var (
	matchRe     = regexp.MustCompile(`^https?://(?:www\.)?riggsfilms\.com(?:/|$)`)
	performerRe = regexp.MustCompile(`/pornstars/([^/?#]+)`)
	sceneLinkRe = regexp.MustCompile(`href="[^"]*/scenes/([^/"]+)/"`)
	slugRe      = regexp.MustCompile(`/scenes/([^/?#]+)/?$`)
	nameRe      = regexp.MustCompile(`<div class="nm">([^<]+)</div>`)
)

// Scraper implements scraper.StudioScraper for riggsfilms.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Riggs Films scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://riggsfilms.com"}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"riggsfilms.com", "riggsfilms.com/scenes/", "riggsfilms.com/pornstars/{slug}/"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

type videoSitemap struct {
	URLs []struct {
		Loc   string `xml:"loc"`
		Video struct {
			Thumbnail   string   `xml:"thumbnail_loc"`
			Title       string   `xml:"title"`
			Description string   `xml:"description"`
			Duration    string   `xml:"duration"`
			Published   string   `xml:"publication_date"`
			Tags        []string `xml:"tag"`
		} `xml:"video"`
	} `xml:"url"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	scenes, err := s.fetchSitemap(ctx, studioURL, now)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}

	if m := performerRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping performer %s", siteID, m[1])
		keep, err := s.performerScenes(ctx, m[1])
		if err != nil {
			send(ctx, out, scraper.Error(err))
			return
		}
		filtered := scenes[:0]
		for _, sc := range scenes {
			if keep[sc.ID] {
				filtered = append(filtered, sc)
			}
		}
		scenes = filtered
	}
	if !send(ctx, out, scraper.Progress(len(scenes))) {
		return
	}

	// The sitemap is not in release order: stored scenes are skipped, not
	// treated as the end.
	fresh := scenes[:0]
	skipped := false
	for _, sc := range scenes {
		if opts.KnownIDs[sc.ID] {
			skipped = true
			continue
		}
		fresh = append(fresh, sc)
	}
	s.fetchCast(ctx, fresh, opts, out)
	if skipped && ctx.Err() == nil {
		send(ctx, out, scraper.StoppedEarly())
	}
}

func (s *Scraper) fetchSitemap(ctx context.Context, studioURL string, now time.Time) ([]models.Scene, error) {
	u := s.base + "/video-sitemap.xml"
	body, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var vs videoSitemap
	if err := parseutil.DecodeXML([]byte(body), &vs); err != nil {
		return nil, scraper.ParseError(u, err)
	}
	var scenes []models.Scene
	for _, e := range vs.URLs {
		m := slugRe.FindStringSubmatch(strings.TrimSpace(e.Loc))
		if m == nil || strings.TrimSpace(e.Video.Title) == "" {
			continue
		}
		sc := models.Scene{
			ID:          m[1],
			SiteID:      siteID,
			StudioURL:   studioURL,
			Title:       clean(e.Video.Title),
			URL:         strings.TrimSpace(e.Loc),
			Description: clean(e.Video.Description),
			Thumbnail:   strings.TrimSpace(e.Video.Thumbnail),
			Studio:      studioName,
			ScrapedAt:   now,
		}
		if n, err := strconv.Atoi(strings.TrimSpace(e.Video.Duration)); err == nil {
			sc.Duration = n
		}
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(e.Video.Published)); err == nil {
			sc.Date = t.UTC()
		}
		for _, t := range e.Video.Tags {
			if t = clean(t); t != "" {
				sc.Tags = append(sc.Tags, t)
			}
		}
		scenes = append(scenes, sc)
	}
	if len(scenes) == 0 {
		return nil, scraper.ParseError(u, errors.New("no scenes in the video sitemap"))
	}
	return scenes, nil
}

func (s *Scraper) performerScenes(ctx context.Context, slug string) (map[string]bool, error) {
	body, err := s.get(ctx, s.base+"/pornstars/"+slug+"/")
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, m := range sceneLinkRe.FindAllStringSubmatch(body, -1) {
		keep[m[1]] = true
	}
	return keep, nil
}

// fetchCast reads each scene page's "Performers in this scene" block — the
// only place the cast is published — and emits the scene. A page that fails
// is reported and the scene emitted without cast.
func (s *Scraper) fetchCast(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	jobs := make(chan models.Scene)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sc := range jobs {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				body, err := s.get(ctx, sc.URL)
				if err != nil {
					if !send(ctx, out, scraper.Error(err)) {
						return
					}
				} else {
					sc.Performers = parseCast(body)
				}
				if !send(ctx, out, scraper.Scene(sc)) {
					return
				}
			}
		}()
	}
feed:
	for _, sc := range scenes {
		select {
		case jobs <- sc:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
}

// castHeading opens the scene's own cast block; the related-scene cards
// further down carry other performers.
const castHeading = `<p class="eb">PERFORMERS IN THIS SCENE</p>`

func parseCast(body string) []string {
	i := strings.Index(body, castHeading)
	if i < 0 {
		return nil
	}
	block := body[i+len(castHeading):]
	if j := strings.Index(block, `<p class="eb">`); j >= 0 {
		block = block[:j]
	}
	var names []string
	for _, n := range nameRe.FindAllStringSubmatch(block, -1) {
		if name := clean(n[1]); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

func (s *Scraper) get(ctx context.Context, u string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := httpx.ReadBody(resp.Body)
	return string(b), err
}
