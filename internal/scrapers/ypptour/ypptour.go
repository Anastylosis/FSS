// Package ypptour scrapes KB Productions sites whose YPP tour theme carries no
// structured data: ManPuppy ("liverpool" theme) and VRAllure ("eastwood").
//
// Both left the Paysite.com Next.js template that paysiteutil parses. Their
// markup has nothing in common, but the backend underneath is the same, so the
// crawl is shared and only the detail-page parser differs per theme:
//
//   - /sitemap.xml names every scene with its release date as <lastmod>, in
//     newest-first order. That is the enumeration — neither listing carries
//     everything a scene needs, and ManPuppy's shows relative dates ("1 week
//     3 days ago") for anything under a few months old.
//   - One detail fetch per scene supplies title, cast, tags and description.
//
// Model pages exist on both sites but list only the newest four or five scenes
// with no pagination, so a /models/{id}-{slug} URL walks the catalogue and
// keeps the scenes whose cast links carry that model id.
package ypptour

import (
	"context"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
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
	// The catalogue is a flat sitemap, so the walk invents its own pages: one
	// chunk of detail fetches at a time, newest first.
	chunkSize      = 24
	defaultWorkers = 4
)

type siteConfig struct {
	id        string
	domain    string
	studio    string
	listPath  string // the site's own listing, for Patterns only
	scenePath string // first path segment of a scene URL
	parse     func(body []byte) detail
}

var sites = []siteConfig{
	{id: "manpuppy", domain: "manpuppy.com", studio: "ManPuppy", listPath: "videos", scenePath: "video", parse: parseLiverpool},
	{id: "vrallure", domain: "vrallure.com", studio: "VR Allure", listPath: "scenes", scenePath: "scenes", parse: parseEastwood},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

type Scraper struct {
	Client  *http.Client
	cfg     siteConfig
	base    string
	matchRe *regexp.Regexp
}

func New(cfg siteConfig) *Scraper {
	return &Scraper{
		Client:  httpx.NewClient(30 * time.Second),
		cfg:     cfg,
		base:    "https://" + cfg.domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.domain) + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.id }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.domain,
		s.cfg.domain + "/" + s.cfg.listPath,
		s.cfg.domain + "/models/{id}-{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- run ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	refs, err := s.sceneRefs(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("%s: %w", s.cfg.id, err)):
		case <-ctx.Done():
		}
		return
	}

	modelID := modelIDOf(studioURL)
	if modelID != "" {
		scraper.Debugf(1, "%s: scraping model %s by filtering %d sitemap scenes", s.cfg.id, modelID, len(refs))
	} else {
		scraper.Debugf(1, "%s: %d scenes in sitemap", s.cfg.id, len(refs))
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}

	// The known-ID check runs on the sitemap entries, before any detail is
	// fetched, so an incremental run costs one request per new scene. In model
	// mode KnownIDs holds only that model's scenes, which are date-ordered like
	// the rest, so stopping at the first one is equally sound there.
	stopped := false
	scraper.Paginate(ctx, opts, s.cfg.id, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		start := (page - 1) * chunkSize
		if start >= len(refs) {
			return scraper.PageResult{Done: true, Continue: true}, nil
		}
		end := min(start+chunkSize, len(refs))
		chunk := refs[start:end]
		for i, r := range chunk {
			if opts.KnownIDs[r.slug] {
				chunk, stopped = chunk[:i], true
				break
			}
		}

		scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.id, len(chunk), workers)
		res := scraper.PageResult{
			Scenes: s.fetchChunk(ctx, chunk, studioURL, modelID, workers, opts.Delay, out),
			Done:   stopped || end >= len(refs),
			// A chunk that filtered to nothing (model mode) or whose every
			// detail failed is not the end of the catalogue.
			Continue: true,
		}
		if modelID == "" {
			res.Total = len(refs)
		}
		return res, nil
	})

	if stopped && ctx.Err() == nil {
		scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.id)
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
}

var modelURLRe = regexp.MustCompile(`^https?://[^/]+/models/(\d+)(?:[-/?#]|$)`)

func modelIDOf(studioURL string) string {
	if m := modelURLRe.FindStringSubmatch(studioURL); m != nil {
		return m[1]
	}
	return ""
}

// ---- sitemap ----

type sceneRef struct {
	slug string
	date time.Time
}

type urlset struct {
	URLs []struct {
		Loc     string `xml:"loc"`
		LastMod string `xml:"lastmod"`
	} `xml:"url"`
}

func (s *Scraper) sceneRefs(ctx context.Context) ([]sceneRef, error) {
	sitemapURL := s.base + "/sitemap.xml"
	body, err := s.get(ctx, sitemapURL)
	if err != nil {
		return nil, err
	}
	var set urlset
	if err := parseutil.DecodeXML(body, &set); err != nil {
		return nil, scraper.ParseError(sitemapURL, err)
	}
	refs := parseSitemap(set, s.cfg.scenePath)
	if len(refs) == 0 {
		return nil, scraper.ParseError(sitemapURL, fmt.Errorf("no /%s/ entries", s.cfg.scenePath))
	}
	return refs, nil
}

// parseSitemap returns the scene entries newest first. The sitemap is already
// in that order today; sorting keeps the early stop honest if it ever is not.
func parseSitemap(set urlset, scenePath string) []sceneRef {
	prefix := "/" + scenePath + "/"
	seen := map[string]bool{}
	var refs []sceneRef
	for _, u := range set.URLs {
		pu, err := url.Parse(strings.TrimSpace(u.Loc))
		if err != nil || !strings.HasPrefix(pu.Path, prefix) {
			continue
		}
		slug := strings.Trim(strings.TrimPrefix(pu.Path, prefix), "/")
		if slug == "" || strings.Contains(slug, "/") || seen[slug] {
			continue
		}
		seen[slug] = true
		refs = append(refs, sceneRef{slug: slug, date: lastModDate(u.LastMod)})
	}
	sort.SliceStable(refs, func(i, j int) bool {
		a, b := refs[i].date, refs[j].date
		if a.IsZero() || b.IsZero() {
			return !a.IsZero() && b.IsZero()
		}
		return a.After(b)
	})
	return refs
}

// lastModDate keeps the calendar date as the site wrote it
// ("2026-09-08T00:00:00-04:00" is the 8th), rather than shifting it into UTC.
func lastModDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if len(s) < 10 {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", s[:10])
	if err != nil {
		return time.Time{}
	}
	return t
}

// ---- details ----

// fetchChunk resolves a chunk concurrently but returns scenes in sitemap order.
func (s *Scraper) fetchChunk(ctx context.Context, refs []sceneRef, studioURL, modelID string, workers int, delay time.Duration, out chan<- scraper.SceneResult) []models.Scene {
	results := make([]*models.Scene, len(refs))
	errs := make([]error, len(refs))

	var wg sync.WaitGroup
	jobs := make(chan int)
	for range min(workers, len(refs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if delay > 0 {
					select {
					case <-time.After(delay):
					case <-ctx.Done():
						return
					}
				}
				results[i], errs[i] = s.fetchScene(ctx, refs[i], studioURL, modelID)
			}
		}()
	}
	func() {
		defer close(jobs)
		for i := range refs {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()

	var scenes []models.Scene
	for i, sc := range results {
		if errs[i] != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("%s: %s: %w", s.cfg.id, refs[i].slug, errs[i])):
			case <-ctx.Done():
				return scenes
			}
			continue
		}
		if sc != nil {
			scenes = append(scenes, *sc)
		}
	}
	return scenes
}

// fetchScene returns nil without error for a scene the model filter rejects.
func (s *Scraper) fetchScene(ctx context.Context, ref sceneRef, studioURL, modelID string) (*models.Scene, error) {
	sceneURL := s.base + "/" + s.cfg.scenePath + "/" + ref.slug
	body, err := s.get(ctx, sceneURL)
	if err != nil {
		return nil, err
	}
	d := s.cfg.parse(body)
	if d.title == "" {
		return nil, scraper.ParseError(sceneURL, fmt.Errorf("no scene title"))
	}
	if modelID != "" && !d.features(modelID) {
		return nil, nil
	}

	date := d.date
	if date.IsZero() {
		date = ref.date
	}
	return &models.Scene{
		ID:          ref.slug,
		SiteID:      s.cfg.id,
		StudioURL:   studioURL,
		Studio:      s.cfg.studio,
		Title:       d.title,
		URL:         sceneURL,
		Thumbnail:   d.thumb,
		Date:        date,
		Duration:    d.duration,
		Description: d.description,
		Performers:  d.performers(),
		Tags:        d.tags,
		ScrapedAt:   time.Now().UTC(),
	}, nil
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- parsing ----

type castMember struct{ id, name string }

type detail struct {
	title, description, thumb string
	date                      time.Time
	duration                  int
	cast                      []castMember
	tags                      []string
}

func (d detail) performers() []string {
	var names []string
	for _, c := range d.cast {
		names = append(names, c.name)
	}
	return names
}

func (d detail) features(modelID string) bool {
	for _, c := range d.cast {
		if c.id == modelID {
			return true
		}
	}
	return false
}

var (
	tagStripRe = regexp.MustCompile(`(?s)<[^>]*>`)
	anchorRe   = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
)

// Liverpool (ManPuppy). The page publishes no date; the sitemap supplies it.
// A scene without a trailer shows a still where the player would be.
var (
	lpTitleRe  = regexp.MustCompile(`(?s)<div class="row item">.*?<h1>(.*?)</h1>`)
	lpTagsRe   = regexp.MustCompile(`(?s)<div class="tags">(.*?)</div>`)
	lpDescRe   = regexp.MustCompile(`(?s)<strong>\s*SCENE INFORMATION:\s*</strong>(.*?)</div>`)
	lpCastRe   = regexp.MustCompile(`(?s)href="[^"]*/models/(\d+)-[^"]*">\s*<span class="name">(.*?)</span>`)
	lpPosterRe = regexp.MustCompile(`<video[^>]*\sposter="([^"]+)"`)
	lpStillRe  = regexp.MustCompile(`(?s)<main>\s*<div class="row">\s*<img src="([^"]+)"`)
)

func parseLiverpool(body []byte) detail {
	page := string(body)
	var d detail
	if m := lpTitleRe.FindStringSubmatch(page); m != nil {
		d.title = cleanText(m[1])
	}
	if m := lpDescRe.FindStringSubmatch(page); m != nil {
		d.description = cleanText(m[1])
	}
	if m := lpPosterRe.FindStringSubmatch(page); m != nil {
		d.thumb = absURL(m[1])
	} else if m := lpStillRe.FindStringSubmatch(page); m != nil {
		d.thumb = absURL(m[1])
	}
	if m := lpTagsRe.FindStringSubmatch(page); m != nil {
		d.tags = anchorTexts(m[1])
	}
	d.cast = castFrom(lpCastRe.FindAllStringSubmatch(page, -1))
	return d
}

// Eastwood (VRAllure). The heading is "<cast> : <title>"; the page carries its
// own date, and a runtime in seconds inside a commented-out block.
var (
	ewTitleRe = regexp.MustCompile(`(?s)<h1 class="latest-scene-title">(.*?)</h1>`)
	ewCastRe  = regexp.MustCompile(`(?s)<p class="model-name">(.*?)</p>`)
	ewModelRe = regexp.MustCompile(`(?s)href="[^"]*/models/(\d+)-[^"]*">(.*?)</a>`)
	ewDateRe  = regexp.MustCompile(`(?s)<p class="publish-date">.*?>\s*([A-Z][a-z]{2} \d{1,2}, \d{4})`)
	ewDurRe   = regexp.MustCompile(`(?s)alt="Duration">\s*(\d+(?:\.\d+)?)\s*<`)
	ewDescRe  = regexp.MustCompile(`(?s)<p class="desc">\s*<span>(.*?)</span>`)
	ewTagRe   = regexp.MustCompile(`(?s)class="label label-tag"[^>]*>(.*?)</a>`)
	ewCoverRe = regexp.MustCompile(`coverimage="([^"]+)"`)
)

func parseEastwood(body []byte) detail {
	page := string(body)
	var d detail
	if m := ewTitleRe.FindStringSubmatch(page); m != nil {
		heading := cleanText(m[1])
		if _, title, ok := strings.Cut(heading, " : "); ok {
			heading = strings.TrimSpace(title)
		}
		d.title = heading
	}
	if m := ewCastRe.FindStringSubmatch(page); m != nil {
		d.cast = castFrom(ewModelRe.FindAllStringSubmatch(m[1], -1))
	}
	if m := ewDateRe.FindStringSubmatch(page); m != nil {
		if t, err := time.Parse("Jan 2, 2006", m[1]); err == nil {
			d.date = t
		}
	}
	if m := ewDurRe.FindStringSubmatch(page); m != nil {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			d.duration = int(math.Round(f))
		}
	}
	if m := ewDescRe.FindStringSubmatch(page); m != nil {
		d.description = cleanText(m[1])
	}
	if m := ewCoverRe.FindStringSubmatch(page); m != nil {
		d.thumb = absURL(m[1])
	} else if og := parseutil.OpenGraph(body)["og:image"]; og != "" {
		d.thumb = absURL(og)
	}
	for _, m := range ewTagRe.FindAllStringSubmatch(page, -1) {
		d.tags = appendUnique(d.tags, cleanText(m[1]))
	}
	return d
}

func castFrom(matches [][]string) []castMember {
	var cast []castMember
	seen := map[string]bool{}
	for _, m := range matches {
		name := cleanText(m[2])
		if name == "" || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		cast = append(cast, castMember{id: m[1], name: name})
	}
	return cast
}

func anchorTexts(block string) []string {
	var out []string
	for _, m := range anchorRe.FindAllStringSubmatch(block, -1) {
		out = appendUnique(out, cleanText(m[1]))
	}
	return out
}

func appendUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func cleanText(s string) string {
	s = html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))
	return strings.Join(strings.Fields(s), " ")
}

// absURL upgrades the CDN's protocol-relative URLs to https.
func absURL(u string) string {
	u = html.UnescapeString(strings.TrimSpace(u))
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}
