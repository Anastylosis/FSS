// Package xsinsvr scrapes XSinsVR (xsinsvr.com), a VR site on a bespoke
// server-rendered Nette (PHP) app. There is no JSON API — /api/fallback/{id}
// only 302s to a player stub — so both listing and detail are parsed from HTML.
//
// XSinsVR is an aggregator as well as a studio: alongside its own SinsVR
// content it licenses catalogues from other brands (18VR, BadoinkVR and
// per-model studios). Each scene carries its originating studio, which is
// recorded in Scene.Studio, so scenes licensed from a brand FSS also scrapes
// directly will legitimately appear in both stores.
//
// The listing (15/page) gives title, URL, thumbnail, preview, performers,
// studio and sometimes duration. The scene id, release date, description and
// tags exist only on the detail page, so every scene needs a detail fetch.
//
// Ordering caveat: the listing is sorted by release date, not by scene id — a
// re-release can carry a low id yet a recent date. The KnownIDs early-stop is
// still sound because it matches on the id set rather than assuming ids
// increase, but nothing here may assume id order.
package xsinsvr

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
	siteID        = "xsinsvr"
	studioName    = "XSinsVR"
	detailWorkers = 4
	dateLayout    = "Jan 02, 2006"
)

var siteBase = "https://xsinsvr.com"

// Scraper implements scraper.StudioScraper for XSinsVR.
type Scraper struct {
	Client *http.Client
}

// New constructs an XSinsVR scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"xsinsvr.com/videos",
		"xsinsvr.com/video/{slug}",
		"xsinsvr.com/model/{slug}",
		"xsinsvr.com/studio/{slug}",
		"xsinsvr.com/tag/scene/{slug}",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?xsinsvr\.com(?:/|$)`)
	// Model, studio and tag pages list their own results and have no /{N}
	// pagination of the /videos kind.
	singlePageRe = regexp.MustCompile(`/(?:model|studio)/[^/]+/?$|/tag/scene/[^/]+/?$`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	if singlePageRe.MatchString(studioURL) {
		scraper.Debugf(1, "%s: scraping filtered page %s", siteID, studioURL)
		s.runSinglePage(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, out, now, opts.Delay)
		return
	}

	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, listingPageURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := parseListing(body)
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.slug] {
				seen[it.slug] = true
				fresh = append(fresh, it)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay, out)
		// The id lives only on the detail page, so a page whose details all
		// failed yields no scenes — Continue keeps that from reading as the end
		// of the catalogue, which --full would then act on by deleting the tail.
		return scraper.PageResult{Scenes: scenes, Continue: len(scenes) == 0}, nil
	})
}

// listingPageURL builds the /videos page URL. Page 1 is the bare path; /videos/0
// exists but renders as the disabled "previous" link.
func listingPageURL(page int) string {
	if page <= 1 {
		return siteBase + "/videos"
	}
	return fmt.Sprintf("%s/videos/%d", siteBase, page)
}

func (s *Scraper) runSinglePage(ctx context.Context, workers int, studioURL string, out chan<- scraper.SceneResult, now time.Time, delay time.Duration) {
	body, err := s.fetchPage(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	items := parseListing(body)
	scraper.Debugf(1, "%s: filtered page has %d videos", siteID, len(items))

	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}
	for _, scene := range s.enrich(ctx, workers, studioURL, items, now, delay, out) {
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}
}

// ---- listing ----

var (
	cardRe = regexp.MustCompile(`<div class="tn-video">`)
	// The template emits `class="main-image"src="…"` with no space before the
	// attribute, so the regex must not require one.
	nameRe    = regexp.MustCompile(`class="tn-video-name" href="/video/([^"]+)"[^>]*>([^<]*)</a>`)
	imageRe   = regexp.MustCompile(`class="main-image"\s*src="([^"]+)"`)
	previewRe = regexp.MustCompile(`<source type="video/mp4"\s*src="([^"]+)"`)
	propsRe   = regexp.MustCompile(`class="tn-video-props">(.*?)</div>`)
	clockRe   = regexp.MustCompile(`(\d{1,2}:\d{2}(?::\d{2})?)`)
	authorRe  = regexp.MustCompile(`class="author" href="/studio/[^"]*"[^>]*>\s*(?:By:)?\s*([^<]+?)\s*</a>`)
	modelsRe  = regexp.MustCompile(`(?s)class="tn-video-models">(.*?)</div>`)
	modelRe   = regexp.MustCompile(`href="/model/[^"]*"[^>]*>\s*([^<]+?)\s*</a>`)
)

type listItem struct {
	slug, title, thumb, preview, studio string
	duration                            int
	performers                          []string
}

func parseListing(body []byte) []listItem {
	page := string(body)
	starts := cardRe.FindAllStringIndex(page, -1)
	items := make([]listItem, 0, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		card := page[loc[0]:end]

		m := nameRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{
			slug:  m[1],
			title: html.UnescapeString(strings.TrimSpace(m[2])),
		}
		if th := imageRe.FindStringSubmatch(card); th != nil {
			it.thumb = th[1]
		}
		if pv := previewRe.FindStringSubmatch(card); pv != nil {
			it.preview = pv[1]
		}
		// The props div holds the resolution badge and, when present, the
		// runtime — it is frequently "<strong>8K</strong> <span></span>".
		if p := propsRe.FindStringSubmatch(card); p != nil {
			if c := clockRe.FindStringSubmatch(p[1]); c != nil {
				it.duration = parseutil.ParseDurationColon(c[1])
			}
		}
		if a := authorRe.FindStringSubmatch(card); a != nil {
			it.studio = html.UnescapeString(strings.TrimSpace(a[1]))
		}
		if mb := modelsRe.FindStringSubmatch(card); mb != nil {
			for _, pm := range modelRe.FindAllStringSubmatch(mb[1], -1) {
				if name := html.UnescapeString(strings.TrimSpace(pm[1])); name != "" {
					it.performers = append(it.performers, name)
				}
			}
		}
		items = append(items, it)
	}
	return items
}

// ---- detail enrichment ----

// enrich fetches each scene's detail page; failures are reported and the scene
// is skipped, since without the detail page it has no id.
func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration, out chan<- scraper.SceneResult) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, it := range items {
		wg.Add(1)
		go func(i int, it listItem) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, delay) {
				return
			}
			scene, err := s.toScene(ctx, studioURL, it, now)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
					return
				}
				return
			}
			scenes[i] = scene
		}(i, it)
	}
	wg.Wait()

	kept := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			kept = append(kept, sc)
		}
	}
	return kept
}

var (
	sceneIDRe    = regexp.MustCompile(`data-scene="(\d+)"`)
	dl8TitleRe   = regexp.MustCompile(`<dl8-video[^>]*title="([^"]*)"`)
	releasedRe   = regexp.MustCompile(`(?s)<strong>Released</strong>\s*<span>\s*<time>\s*([^<]+?)\s*</time>`)
	starringRe   = regexp.MustCompile(`(?s)<strong>Starring</strong>\s*<span>(.*?)</span>`)
	tinyLinkRe   = regexp.MustCompile(`href="/model/[^"]*"[^>]*>\s*([^<]+?)\s*</a>`)
	studioBlkRe  = regexp.MustCompile(`(?s)<strong>Studio</strong>\s*<span>.*?/studio/[^"]*"[^>]*>\s*([^<]+?)\s*</a>`)
	specsRe      = regexp.MustCompile(`(?s)<strong>Specifications</strong>\s*<span>\s*([^<]+?)\s*</span>`)
	detailDescRe = regexp.MustCompile(`(?s)class="[^"]*video-detail__desc[^"]*"[^>]*>.*?<div class="small">(.*?)</div>`)
	tagRe        = regexp.MustCompile(`href="/tag/scene/[^"]*"[^>]*>\s*([^<]+?)\s*</a>`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
)

// toScene fetches the detail page, which is where the scene id lives — without
// it there is no scene to emit, so a failure is reported rather than swallowed.
func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) (models.Scene, error) {
	sceneURL := siteBase + "/video/" + it.slug

	body, err := s.fetchPage(ctx, sceneURL)
	if err != nil {
		return models.Scene{}, err
	}
	detail := string(body)

	m := sceneIDRe.FindStringSubmatch(detail)
	if m == nil {
		return models.Scene{}, scraper.ParseError(sceneURL, fmt.Errorf("no scene id on the page"))
	}

	scene := models.Scene{
		ID:         m[1],
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        sceneURL,
		Duration:   it.duration,
		Thumbnail:  it.thumb,
		Preview:    it.preview,
		Performers: it.performers,
		Studio:     studioName,
		ScrapedAt:  now,
	}

	// The originating brand — XSinsVR licenses catalogues from other studios.
	if it.studio != "" {
		scene.Studio = it.studio
	} else if sm := studioBlkRe.FindStringSubmatch(detail); sm != nil {
		scene.Studio = html.UnescapeString(strings.TrimSpace(sm[1]))
	}

	if scene.Title == "" {
		if t := dl8TitleRe.FindStringSubmatch(detail); t != nil {
			scene.Title = html.UnescapeString(strings.TrimSpace(t[1]))
		}
	}
	if d := releasedRe.FindStringSubmatch(detail); d != nil {
		if ts, err := time.Parse(dateLayout, strings.TrimSpace(d[1])); err == nil {
			scene.Date = ts.UTC()
		}
	}
	if sb := starringRe.FindStringSubmatch(detail); sb != nil {
		var names []string
		for _, pm := range tinyLinkRe.FindAllStringSubmatch(sb[1], -1) {
			if name := html.UnescapeString(strings.TrimSpace(pm[1])); name != "" && !contains(names, name) {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			scene.Performers = names
		}
	}
	if sp := specsRe.FindStringSubmatch(detail); sp != nil {
		scene.Resolution = html.UnescapeString(strings.TrimSpace(sp[1]))
	}
	if dd := detailDescRe.FindStringSubmatch(detail); dd != nil {
		text := html.UnescapeString(tagStripRe.ReplaceAllString(dd[1], " "))
		scene.Description = strings.Join(strings.Fields(text), " ")
	}
	// Tags repeat on the page, so dedupe.
	for _, tm := range tagRe.FindAllStringSubmatch(detail, -1) {
		tag := html.UnescapeString(strings.TrimSpace(tm[1]))
		if tag != "" && !contains(scene.Tags, tag) {
			scene.Tags = append(scene.Tags, tag)
		}
	}

	return scene, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
