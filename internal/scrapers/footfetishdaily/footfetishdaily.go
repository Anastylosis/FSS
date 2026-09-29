// Package footfetishdaily scrapes Foot Fetish Daily (footfetishdaily.com), a
// site on the custom "Kickass Pictures" CMS. The /videos/{N} listing carries
// one card per scene with the /update/{id}/{slug} link, title and thumbnail;
// the per-scene /update/{id}/{slug} detail page carries a schema.org
// VideoObject (JSON-LD) with the title, date, description and thumbnail, plus
// /model/{id}/{slug} performer links.
package footfetishdaily

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
	siteID     = "footfetishdaily"
	studioName = "Foot Fetish Daily"
	siteBase   = "https://www.footfetishdaily.com"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?footfetishdaily\.com(?:/|$)`)

type Scraper struct {
	client *http.Client
	// base is the site root used for *fetches*, a field so offline tests can
	// drive the whole walk instead of only the parsers.
	base string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"footfetishdaily.com",
		"footfetishdaily.com/videos",
		"footfetishdaily.com/update/{id}/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listingScene struct {
	id   string
	slug string
	url  string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listingScene)
	var wg sync.WaitGroup
	scraper.Debugf(1, "footfetishdaily: fetching detail pages with %d workers", workers)
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
		s.enqueueListing(ctx, opts, out, work)
	}()

	wg.Wait()
}

func (s *Scraper) enqueueListing(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "footfetishdaily: fetching page %d", page)
		pageURL := fmt.Sprintf("%s/videos/%d", s.base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}
		scenes := parseListing(s.base, body)
		if len(scenes) == 0 {
			scraper.Debugf(1, "footfetishdaily: page %d empty, stopping", page)
			return
		}
		if !s.enqueue(ctx, scenes, opts, out, work) {
			return
		}
	}
}

// enqueue sends scenes to the work channel, honouring KnownIDs early-stop.
// Returns false when the caller should stop paginating.
func (s *Scraper) enqueue(ctx context.Context, scenes []listingScene, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) bool {
	for _, ls := range scenes {
		if opts.KnownIDs[ls.id] {
			scraper.Debugf(1, "footfetishdaily: hit known ID %s, stopping early", ls.id)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return false
		}
		select {
		case work <- ls:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

var (
	updateLinkRe = regexp.MustCompile(`href="(/update/(\d+)/([^"]+))"`)
	modelLinkRe  = regexp.MustCompile(`href="/model/\d+/[^"]*"[^>]*>([^<]+)</a>`)
)

// parseListing extracts the /update/{id}/{slug} link from each card, deduped
// by numeric id (the link appears on both the thumbnail and the title).
func parseListing(base string, body []byte) []listingScene {
	page := string(body)
	matches := updateLinkRe.FindAllStringSubmatch(page, -1)
	scenes := make([]listingScene, 0, len(matches))
	seen := map[string]bool{}

	for _, m := range matches {
		id := m[2]
		if seen[id] {
			continue
		}
		seen[id] = true
		scenes = append(scenes, listingScene{
			id:   id,
			slug: m[3],
			url:  base + m[1],
		})
	}
	return scenes
}

type detailData struct {
	title       string
	date        time.Time
	description string
	thumbnail   string
	performers  []string
}

func parseDetail(body []byte) detailData {
	var d detailData
	page := string(body)

	if vo := parseutil.ExtractVideoObject(body); vo != nil {
		d.title = strings.TrimSpace(html.UnescapeString(vo.Name))
		d.description = strings.TrimSpace(html.UnescapeString(vo.Description))
		d.thumbnail = vo.ThumbnailURL
		if vo.DatePublished != "" {
			if t, err := parseutil.TryParseDate(vo.DatePublished, time.RFC3339); err == nil {
				d.date = t.UTC()
			}
		}
	}

	// Fall back to OpenGraph for any missing core fields.
	og := parseutil.OpenGraph(body)
	if d.title == "" {
		if v := og["og:title"]; v != "" {
			d.title = strings.TrimSpace(html.UnescapeString(v))
		}
	}
	if d.description == "" {
		if v := og["og:description"]; v != "" {
			d.description = strings.TrimSpace(html.UnescapeString(v))
		}
	}

	seen := map[string]bool{}
	for _, m := range modelLinkRe.FindAllStringSubmatch(page, -1) {
		name := strings.TrimSpace(html.UnescapeString(m[1]))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		d.performers = append(d.performers, name)
	}
	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	scene := models.Scene{
		ID:        ls.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       ls.url,
		Studio:    studioName,
		ScrapedAt: time.Now().UTC(),
	}

	body, err := s.fetchPage(ctx, ls.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", ls.id, err)
	}
	d := parseDetail(body)
	scene.Title = d.title
	scene.Date = d.date
	scene.Description = d.description
	scene.Thumbnail = d.thumbnail
	scene.Performers = d.performers
	if scene.Title == "" {
		scene.Title = slugToTitle(ls.slug)
	}
	return scene, nil
}

func slugToTitle(slug string) string {
	return strings.ReplaceAll(slug, "_", " ")
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
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
