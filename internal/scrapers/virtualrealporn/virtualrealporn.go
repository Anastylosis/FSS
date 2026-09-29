// Package virtualrealporn scrapes the VirtualRealPorn VR network — a family of
// sites on the shared "VirtualRealHub" platform: VirtualRealPorn,
// VirtualRealGay, VirtualRealTrans, VirtualRealJapan and VirtualRealPassion.
// It is a table-driven package: one scraper is registered per site in init().
//
// The public scene catalogue is exposed through a sitemap.xml index pointing
// at a single per-site videos_sitemap.xml, which lists every scene URL in
// publish order (oldest first). The scraper walks the file newest-first
// (reversed) and enriches each scene from its detail page, which carries a
// schema.org VideoObject JSON-LD block with title, description, duration and
// upload date. As of the 2026 platform migration the JSON-LD no longer
// includes performers or keywords, so those are scraped from the page's own
// "VR Pornstars" (`vd-pornstar__name`) and tag (`vd-tags__tag`) markup instead.
//
// VirtualRealAmateur (virtualrealamateurporn.com) is intentionally not covered:
// the domain 301-redirects to virtualrealporn.com and no longer publishes its
// own catalogue.
package virtualrealporn

import (
	"context"
	"encoding/json"
	"encoding/xml"
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

// pageSize is how many scene detail pages are fetched per pagination chunk.
// It mirrors the network's own listing page size (24) so progress and the
// KnownIDs early-stop behave like a normal page-numbered scraper.
const pageSize = 24

// SiteConfig describes one VirtualReal network site served by this package.
type SiteConfig struct {
	SiteID     string // stable lowercase id, e.g. "virtualrealporn"
	Domain     string // bare domain, e.g. "virtualrealporn.com"
	StudioName string // display name, e.g. "VirtualRealPorn"
}

var sites = []SiteConfig{
	{SiteID: "virtualrealporn", Domain: "virtualrealporn.com", StudioName: "VirtualRealPorn"},
	{SiteID: "virtualrealgay", Domain: "virtualrealgay.com", StudioName: "VirtualRealGay"},
	{SiteID: "virtualrealtrans", Domain: "virtualrealtrans.com", StudioName: "VirtualRealTrans"},
	{SiteID: "virtualrealjapan", Domain: "virtualrealjapan.com", StudioName: "VirtualRealJapan"},
	{SiteID: "virtualrealpassion", Domain: "virtualrealpassion.com", StudioName: "VirtualRealPassion"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newFor(cfg.SiteID))
	}
}

// newFor builds the registered scraper for a given site id. It is also used by
// the integration tests.
func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return New(cfg)
		}
	}
	return nil
}

// Scraper implements scraper.StudioScraper for a single VirtualReal site.
type Scraper struct {
	cfg     SiteConfig
	Client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

// New constructs a Scraper for the given site config.
func New(cfg SiteConfig) *Scraper {
	escaped := regexp.QuoteMeta(cfg.Domain)
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		base:    "https://" + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + escaped + `(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/latest-videos/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

// sitemapURLSet maps the Yoast sitemap <urlset> for a single sitemap file.
type sitemapURLSet struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

// sitemapIndex maps the Yoast sitemap index, used to discover every paginated
// pelicula sitemap file (pelicula-sitemap.xml, pelicula-sitemap2.xml, …).
type sitemapIndex struct {
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: discovering scene sitemaps", s.cfg.SiteID)
	urls, err := s.collectSceneURLs(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	if len(urls) == 0 {
		return
	}
	scraper.Debugf(1, "%s: %d scenes in catalogue", s.cfg.SiteID, len(urls))

	now := time.Now().UTC()
	totalPages := (len(urls) + pageSize - 1) / pageSize

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		start := (page - 1) * pageSize
		if start >= len(urls) {
			return scraper.PageResult{}, nil
		}
		end := start + pageSize
		if end > len(urls) {
			end = len(urls)
		}
		chunk := urls[start:end]

		total := 0
		if page == 1 {
			total = len(urls)
		}

		scenes := s.fetchDetails(ctx, chunk, opts, now)
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   page >= totalPages,
		}, nil
	})
}

// collectSceneURLs returns every scene detail URL for the site, ordered
// newest-first. The videos_sitemap.xml file(s) list scenes oldest-first; both
// the file order and the within-file order are reversed so the freshest scene
// comes first, which is what KnownIDs early-stop relies on. In practice each
// site has exactly one videos_sitemap.xml, but the multi-file reversal is kept
// in case the platform paginates it (videos_sitemap2.xml, …) in the future.
func (s *Scraper) collectSceneURLs(ctx context.Context) ([]string, error) {
	files, err := s.sceneSitemapFiles(ctx)
	if err != nil {
		return nil, err
	}

	var urls []string
	// Files are reversed: the highest-numbered sitemap holds the newest scenes.
	for i := len(files) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		body, err := s.fetchPage(ctx, files[i])
		if err != nil {
			return nil, err
		}
		var set sitemapURLSet
		if err := xml.Unmarshal(body, &set); err != nil {
			return nil, fmt.Errorf("parse sitemap %s: %w", files[i], err)
		}
		// Reverse within the file so the newest scene leads.
		for j := len(set.URLs) - 1; j >= 0; j-- {
			loc := strings.TrimSpace(set.URLs[j].Loc)
			if loc != "" {
				urls = append(urls, loc)
			}
		}
	}
	return urls, nil
}

// sceneSitemapFiles discovers the videos_sitemap file URLs from the sitemap
// index. The files are returned in index order (oldest first).
func (s *Scraper) sceneSitemapFiles(ctx context.Context) ([]string, error) {
	body, err := s.fetchPage(ctx, s.base+"/sitemap.xml")
	if err != nil {
		return nil, err
	}
	var idx sitemapIndex
	if err := xml.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("parse sitemap index: %w", err)
	}
	var files []string
	for _, sm := range idx.Sitemaps {
		loc := strings.TrimSpace(sm.Loc)
		if strings.Contains(loc, "videos_sitemap") {
			files = append(files, loc)
		}
	}
	return files, nil
}

// fetchDetails enriches each scene URL from its detail page with a worker pool.
// Order is preserved so Paginate's KnownIDs early-stop fires on the right
// scene; known IDs become lightweight stubs (no detail fetch) and detail-fetch
// failures are dropped.
func (s *Scraper) fetchDetails(ctx context.Context, urls []string, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(urls), workers)

	results := make([]models.Scene, len(urls))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, u := range urls {
		if ctx.Err() != nil {
			break
		}
		id := sceneID(u)
		if opts.KnownIDs[id] {
			results[i] = models.Scene{ID: id, SiteID: s.cfg.SiteID}
			continue
		}
		wg.Add(1)
		go func(idx int, rawURL, id string) {
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

			body, err := s.fetchPage(ctx, rawURL)
			if err != nil {
				scraper.Debugf(1, "%s: detail %s failed: %v (skipping)", s.cfg.SiteID, id, err)
				return
			}
			if sc, ok := s.toScene(body, rawURL, id, now); ok {
				results[idx] = sc
			}
		}(i, u, id)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, sc := range results {
		if sc.ID == "" { // failed fetch
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

// ldStrings decodes a JSON-LD property that may be a string, a
// comma-separated string, an array of strings, or an array of {name} objects.
// Never errors: `genre` changing from string to array used to fail the whole
// ldMovie, silently dropping every scene on all five sites.
type ldStrings []string

func (l *ldStrings) UnmarshalJSON(b []byte) error {
	var single string
	if err := json.Unmarshal(b, &single); err == nil {
		for _, part := range strings.Split(single, ",") {
			if v := strings.TrimSpace(part); v != "" {
				*l = append(*l, v)
			}
		}
		return nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(b, &items); err != nil {
		return nil
	}
	for _, it := range items {
		var s string
		if err := json.Unmarshal(it, &s); err == nil {
			if v := strings.TrimSpace(s); v != "" {
				*l = append(*l, v)
			}
			continue
		}
		var obj struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(it, &obj); err == nil {
			if v := strings.TrimSpace(obj.Name); v != "" {
				*l = append(*l, v)
			}
		}
	}
	return nil
}

// ldMovie is the subset of the schema.org Movie/VideoObject JSON-LD block on a
// detail page. Both block shapes are read: the old Movie one spells the cast
// "actors" and tags "keywords", the current VideoObject one "actor"/"genre".
type ldMovie struct {
	Type          string          `json:"@type"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Image         json.RawMessage `json:"image"`
	ThumbnailURL  string          `json:"thumbnailUrl"`
	Duration      string          `json:"duration"`
	DatePublished string          `json:"datePublished"`
	UploadDate    string          `json:"uploadDate"`
	Keywords      ldStrings       `json:"keywords"`
	Genre         ldStrings       `json:"genre"`
	Actors        ldStrings       `json:"actors"`
	Actor         ldStrings       `json:"actor"`
}

var (
	ldJSONRe = regexp.MustCompile(`(?s)<script type="application/ld\+json"[^>]*>(.*?)</script>`)
	slugRe   = regexp.MustCompile(`/([a-z0-9-]+)/?$`)

	// pornstarNameRe and tagsRe scrape performers/tags directly from the
	// detail page's "VR Pornstars" and tag-list sections — the current
	// VideoObject JSON-LD no longer carries this data.
	pornstarNameRe = regexp.MustCompile(`<span class="vd-pornstar__name">([^<]+)</span>`)
	tagsRe         = regexp.MustCompile(`<a[^>]*class="vd-tags__tag"[^>]*>([^<]+)</a>`)
)

func (s *Scraper) toScene(body []byte, rawURL, id string, now time.Time) (models.Scene, bool) {
	movie, ok := extractMovie(body)
	if !ok {
		return models.Scene{}, false
	}

	performers := actorNames(movie.Actors, movie.Actor)
	if len(performers) == 0 {
		performers = extractPornstarNames(body)
	}
	tags := sceneTags(movie.Keywords, movie.Genre)
	if len(tags) == 0 {
		tags = extractTags(body)
	}
	thumbnail := firstImage(movie.Image)
	if thumbnail == "" {
		thumbnail = cleanText(movie.ThumbnailURL)
	}

	scene := models.Scene{
		ID:          id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   s.base,
		Title:       cleanTitle(movie.Name),
		URL:         rawURL,
		Studio:      s.cfg.StudioName,
		Description: cleanText(movie.Description),
		Thumbnail:   thumbnail,
		Duration:    parseDuration(movie.Duration),
		Performers:  performers,
		Tags:        tags,
		ScrapedAt:   now,
	}
	publishedAt := movie.DatePublished
	if publishedAt == "" {
		publishedAt = movie.UploadDate
	}
	if t, err := parseutil.TryParseDate(strings.TrimSpace(publishedAt),
		time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02"); err == nil {
		scene.Date = t.UTC()
	}
	if scene.Title == "" {
		scene.Title = titleFromSlug(id)
	}
	return scene, true
}

// parseDuration handles both the current ISO 8601 VideoObject duration
// ("PT800S") and the older colon-separated Movie duration ("28:36").
func parseDuration(s string) int {
	if strings.HasPrefix(strings.TrimSpace(s), "PT") {
		return parseutil.ParseDurationISO(s)
	}
	return parseutil.ParseDurationColon(s)
}

// extractPornstarNames scrapes performer names from the detail page's "VR
// Pornstars" section, stripping the " VR" suffix every name carries there.
func extractPornstarNames(body []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range pornstarNameRe.FindAllSubmatch(body, -1) {
		name := strings.TrimSpace(html.UnescapeString(string(m[1])))
		name = strings.TrimSuffix(name, " VR")
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// extractTags scrapes the tag list directly from the detail page's tag
// section markup.
func extractTags(body []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range tagsRe.FindAllSubmatch(body, -1) {
		tag := strings.TrimSpace(html.UnescapeString(string(m[1])))
		key := strings.ToLower(tag)
		if tag != "" && !seen[key] {
			seen[key] = true
			out = append(out, tag)
		}
	}
	return out
}

// extractMovie parses the first schema.org Movie (or VideoObject) JSON-LD block.
func extractMovie(body []byte) (ldMovie, bool) {
	for _, m := range ldJSONRe.FindAllSubmatch(body, -1) {
		blk := strings.TrimSpace(string(m[1]))
		if !strings.Contains(blk, `"Movie"`) && !strings.Contains(blk, `"VideoObject"`) {
			continue
		}
		var mv ldMovie
		if err := json.Unmarshal([]byte(blk), &mv); err != nil {
			continue
		}
		if mv.Type == "Movie" || mv.Type == "VideoObject" {
			return mv, true
		}
	}
	return ldMovie{}, false
}

// sceneID returns the trailing slug of a scene URL, used as the stable scene ID.
func sceneID(rawURL string) string {
	if m := slugRe.FindStringSubmatch(rawURL); m != nil {
		return m[1]
	}
	return strings.TrimRight(rawURL, "/")
}

// cleanTitle strips the site suffix (e.g. " | VirtualRealPorn VR Porn video")
// that the CMS appends to every JSON-LD name.
func cleanTitle(name string) string {
	name = cleanText(name)
	if i := strings.Index(name, " | "); i >= 0 {
		name = name[:i]
	}
	return strings.TrimSpace(name)
}

// titleFromSlug builds a readable fallback title from the URL slug.
func titleFromSlug(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func actorNames(actors ...ldStrings) []string {
	seen := make(map[string]bool)
	var out []string
	for _, list := range actors {
		for _, a := range list {
			n := cleanText(a)
			if n == "" || seen[strings.ToLower(n)] {
				continue
			}
			seen[strings.ToLower(n)] = true
			out = append(out, n)
		}
	}
	return out
}

// sceneTags: on the old block "keywords" held the tags and "genre" the generic
// set to drop; the current block has no keywords and puts the real tags in
// genre. So genre is a deny-list only when keywords is present.
func sceneTags(keywords, genre ldStrings) []string {
	if len(keywords) == 0 {
		return dedupeTags(genre, nil)
	}
	generic := make(map[string]bool, len(genre))
	for _, g := range genre {
		generic[strings.ToLower(strings.TrimSpace(g))] = true
	}
	return dedupeTags(keywords, generic)
}

func dedupeTags(tags ldStrings, skip map[string]bool) []string {
	seen := make(map[string]bool)
	var out []string
	for _, k := range tags {
		tag := cleanText(k)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if skip[key] || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, tag)
	}
	return out
}

// firstImage returns the first image URL from the JSON-LD "image" field, which
// may be a bare string or an array of strings.
func firstImage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return cleanText(single)
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		return cleanText(arr[0])
	}
	return ""
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
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
