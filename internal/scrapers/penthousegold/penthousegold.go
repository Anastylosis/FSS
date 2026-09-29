// Package penthousegold scrapes the public "latest scenes" surface of
// penthousegold.com (ElevatedX CMS).
//
// The full archive is paywalled: category, model and "All" listings expose
// only a handful of free scenes, and there is no public full-archive
// pagination. This is therefore a latest-only incremental scraper — it
// gathers the newest scene URLs from the RSS feed (sitemap.xml) plus the
// homepage, deduplicates them, and worker-pool fetches each public detail
// page (/scenes/{slug}_vids.html) for its schema.org VideoObject microdata.
package penthousegold

import (
	"context"
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

func init() { scraper.Register(New()) }

const studioName = "Penthouse Gold"

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://www.penthousegold.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return "penthousegold" }

func (s *Scraper) Patterns() []string {
	return []string{
		"penthousegold.com",
		"penthousegold.com/scenes/{slug}_vids.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?penthousegold\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// sceneLinkRe matches a public scene detail link (/scenes/{slug}_vids.html)
	// in either the RSS feed or the homepage HTML. Gallery/extra links use the
	// _highres.html suffix and are intentionally excluded. The leading
	// delimiter alternation anchors the host to a boundary (start, whitespace,
	// quote, paren, or angle bracket — the latter covers <loc> in the sitemap)
	// so the URL cannot be embedded in the path/query of an arbitrary host.
	sceneLinkRe = regexp.MustCompile(`(?:^|[\s"'(<>])https?://(?:www\.)?penthousegold\.com/scenes/([A-Za-z0-9][A-Za-z0-9-]*)_vids\.html`)

	// relSceneLinkRe matches the same link in relative form (homepage cards).
	relSceneLinkRe = regexp.MustCompile(`(?:^|["'(])/scenes/([A-Za-z0-9][A-Za-z0-9-]*)_vids\.html`)

	itempropRe = regexp.MustCompile(`<meta[^>]*itemprop="([a-zA-Z]+)"[^>]*content="([^"]*)"`)

	// castRe extracts cast performers: a model link wrapped in an <h3> that is
	// immediately followed by the per-pornstar details list. This isolates the
	// scene cast from unrelated model links elsewhere on the page (nav, related).
	castRe = regexp.MustCompile(`(?s)<h3>\s*<a[^>]*href="[^"]*/models/[a-z][a-z0-9-]+\.html"[^>]*>([^<]+)</a>\s*</h3>\s*<ul class="details-pornstar"`)

	// sceneTagsRe / tagItemRe pull the scene's category tags from the
	// <ul class="scene-tags"> block.
	sceneTagsRe = regexp.MustCompile(`(?s)<ul class="scene-tags">(.*?)</ul>`)
	tagItemRe   = regexp.MustCompile(`/categories/[a-z0-9-]+\.html"[^>]*>([^<]+)</a>`)
)

// sceneRef is a discovered scene: its slug (the stable ID) and detail URL.
type sceneRef struct {
	id  string
	url string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	refs := s.discover(ctx, out)
	if ctx.Err() != nil {
		return
	}
	if len(refs) == 0 {
		return
	}

	scraper.Debugf(1, "penthousegold: discovered %d public scenes", len(refs))
	select {
	case out <- scraper.Progress(len(refs)):
	case <-ctx.Done():
		return
	}

	now := time.Now().UTC()
	scenes := s.fetchDetails(ctx, refs, studioURL, opts, now)

	for _, sc := range scenes {
		if sc.ID == "" {
			continue // failed detail fetch
		}
		if opts.KnownIDs[sc.ID] {
			scraper.Debugf(1, "penthousegold: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(sc):
		case <-ctx.Done():
			return
		}
	}
}

// discover gathers the newest public scene refs from the RSS feed and the
// homepage, in feed-then-homepage order, deduplicated by slug
// (case-insensitive). On a fetch error it reports it on the channel but keeps
// whatever it found from the other source.
func (s *Scraper) discover(ctx context.Context, out chan<- scraper.SceneResult) []sceneRef {
	seen := make(map[string]bool)
	var refs []sceneRef

	add := func(body []byte) {
		for _, m := range sceneLinkRe.FindAllSubmatch(body, -1) {
			s.addRef(&refs, seen, string(m[1]))
		}
		for _, m := range relSceneLinkRe.FindAllSubmatch(body, -1) {
			s.addRef(&refs, seen, string(m[1]))
		}
	}

	scraper.Debugf(1, "penthousegold: fetching RSS feed")
	if body, err := s.fetchPage(ctx, s.base+"/sitemap.xml"); err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
			return refs
		}
	} else {
		add(body)
	}

	if ctx.Err() != nil {
		return refs
	}

	scraper.Debugf(1, "penthousegold: fetching homepage")
	if body, err := s.fetchPage(ctx, s.base+"/"); err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
	} else {
		add(body)
	}

	return refs
}

func (s *Scraper) addRef(refs *[]sceneRef, seen map[string]bool, slug string) {
	key := strings.ToLower(slug)
	if slug == "" || seen[key] {
		return
	}
	seen[key] = true
	*refs = append(*refs, sceneRef{
		id:  slug,
		url: s.base + "/scenes/" + slug + "_vids.html",
	})
}

func (s *Scraper) fetchDetails(ctx context.Context, refs []sceneRef, studioURL string, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "penthousegold: fetching %d details with %d workers", len(refs), workers)

	results := make([]models.Scene, len(refs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, ref := range refs {
		if ctx.Err() != nil {
			break
		}
		// Known IDs become lightweight stubs (no detail fetch) so the
		// ordered early-stop in run() still fires.
		if opts.KnownIDs[ref.id] {
			results[i] = models.Scene{ID: ref.id, SiteID: "penthousegold"}
			continue
		}
		wg.Add(1)
		go func(idx int, ref sceneRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if !scraper.Pace(ctx, opts.Delay) {
				// Leaving results[idx] unwritten is deliberate: the zero value has an
				// empty ID and the consumer below skips those, so a cancelled fetch
				// contributes no scene. Do not "fix" this by writing a stub here —
				// a populated slot is emitted as a real scene with no title or URL.
				return
			}

			body, err := s.fetchPage(ctx, ref.url)
			if err != nil {
				scraper.Debugf(1, "penthousegold: detail %s failed: %v (skipping)", ref.id, err)
				return
			}
			results[idx] = parseDetail(body, ref, studioURL, now)
		}(i, ref)
	}
	wg.Wait()

	return results
}

func parseDetail(body []byte, ref sceneRef, studioURL string, now time.Time) models.Scene {
	props := make(map[string]string)
	for _, m := range itempropRe.FindAllSubmatch(body, -1) {
		props[string(m[1])] = cleanText(string(m[2]))
	}

	title := props["name"]
	if title == "" {
		og := parseutil.OpenGraph(body)
		title = cleanText(og["title"])
	}

	description := props["description"]
	if description == "" {
		og := parseutil.OpenGraph(body)
		description = cleanText(og["description"])
	}

	thumbnail := props["thumbnailUrl"]
	if thumbnail == "" {
		og := parseutil.OpenGraph(body)
		thumbnail = og["image"]
	}

	scene := models.Scene{
		ID:          ref.id,
		SiteID:      "penthousegold",
		StudioURL:   studioURL,
		Studio:      studioName,
		Title:       title,
		URL:         ref.url,
		Description: description,
		Thumbnail:   thumbnail,
		Preview:     props["contentURL"],
		Performers:  parseCast(body),
		Tags:        parseTags(body),
		Duration:    parseutil.ParseDurationISO(props["duration"]),
		ScrapedAt:   now,
	}

	// uploadDate is MM/DD/YYYY on ElevatedX; also tolerate ISO if it appears.
	if t, err := parseutil.TryParseDate(props["uploadDate"], "01/02/2006", "2006-01-02", time.RFC3339); err == nil {
		scene.Date = t.UTC()
	}

	return scene
}

func parseCast(body []byte) []string {
	seen := make(map[string]bool)
	var names []string
	for _, m := range castRe.FindAllSubmatch(body, -1) {
		name := cleanText(string(m[1]))
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

func parseTags(body []byte) []string {
	block := sceneTagsRe.FindSubmatch(body)
	if block == nil {
		return nil
	}
	seen := make(map[string]bool)
	var tags []string
	for _, m := range tagItemRe.FindAllSubmatch(block[1], -1) {
		tag := cleanText(string(m[1]))
		if tag != "" && !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	return tags
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

func cleanText(s string) string {
	return html.UnescapeString(strings.TrimSpace(s))
}
