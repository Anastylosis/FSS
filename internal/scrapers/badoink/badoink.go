// Package badoink scrapes the BaDoink VR network — BaDoinkVR, 18VR,
// VRCosplayX, BabeVR and RealVR — which share one modern CMS. It is a
// table-driven package: one scraper is registered per site in init().
//
// The public listing is paginated newest-first at
// /{listPath}/{N}?order=newest (page 1 omits the /{N}). Each listing card
// links to a detail page under /{videoPath}/{slug}-{id}/ whose numeric trailing
// id is the scene ID. Detail pages carry a schema.org VideoObject JSON-LD block
// (title, description, thumbnail, preview, actors, upload date); duration and
// scene tags are read from the surrounding HTML.
package badoink

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one BaDoink-network site served by this package.
//
// ListPath is the paginated catalogue path (without leading slash), e.g.
// "vrpornvideos". VideoPath is the detail-page path segment, e.g.
// "vrpornvideo". Most sites share "vrpornvideos"/"vrpornvideo"; VRCosplayX
// uses "cosplaypornvideos"/"cosplaypornvideo".
type SiteConfig struct {
	SiteID     string // stable lowercase id, e.g. "badoinkvr"
	Domain     string // bare domain, e.g. "badoinkvr.com"
	StudioName string // display name, e.g. "BaDoinkVR"
	ListPath   string // catalogue path segment, e.g. "vrpornvideos"
	VideoPath  string // detail path segment, e.g. "vrpornvideo"
}

var sites = []SiteConfig{
	{SiteID: "badoinkvr", Domain: "badoinkvr.com", StudioName: "BaDoinkVR", ListPath: "vrpornvideos", VideoPath: "vrpornvideo"},
	{SiteID: "18vr", Domain: "18vr.com", StudioName: "18VR", ListPath: "vrpornvideos", VideoPath: "vrpornvideo"},
	{SiteID: "vrcosplayx", Domain: "vrcosplayx.com", StudioName: "VRCosplayX", ListPath: "cosplaypornvideos", VideoPath: "cosplaypornvideo"},
	{SiteID: "babevr", Domain: "babevr.com", StudioName: "BabeVR", ListPath: "vrpornvideos", VideoPath: "vrpornvideo"},
	{SiteID: "realvr", Domain: "realvr.com", StudioName: "RealVR", ListPath: "vrpornvideos", VideoPath: "vrpornvideo"},
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

// Scraper implements scraper.StudioScraper for a single BaDoink-network site.
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
		s.cfg.Domain + "/" + s.cfg.ListPath,
		s.cfg.Domain + "/" + s.cfg.VideoPath + "/{slug}-{id}/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

type listItem struct {
	id    string
	slug  string // full "{slug}-{id}" path segment
	url   string
	title string
}

type detailData struct {
	title       string
	description string
	thumbnail   string
	preview     string
	date        time.Time
	duration    int
	performers  []string
	tags        []string
}

// cardLinkRe pulls the "{slug}-{id}" path segment from a listing card title
// link, scoped to the per-site video path. The id is the trailing number.
func (s *Scraper) cardLinkRe() *regexp.Regexp {
	return regexp.MustCompile(`href="/` + regexp.QuoteMeta(s.cfg.VideoPath) + `/([a-z0-9_-]+?-(\d+))/?"`)
}

// pageNumRe finds catalogue pagination page numbers for a total estimate.
func (s *Scraper) pageNumRe() *regexp.Regexp {
	return regexp.MustCompile(`href="/` + regexp.QuoteMeta(s.cfg.ListPath) + `/(\d+)`)
}

var (
	// cardTitleRe pulls the card title from the title attribute on the link.
	cardTitleRe = regexp.MustCompile(`title="([^"]*)"`)

	// detailDurRe reads the ISO-8601 duration from the schema.org itemprop.
	detailDurRe = regexp.MustCompile(`itemprop="duration"\s+content="(PT[^"]+)"`)
	// detailTagRe pulls scene category links rendered as video-tag chips.
	detailTagRe = regexp.MustCompile(`href="[^"]*/category/[^"]+"\s+class="video-tag">([^<]+)</a>`)
	// detailActorRe pulls performer links flagged with itemprop="actor".
	detailActorRe = regexp.MustCompile(`href="[^"]*/vr-pornstar/[^"]+"[^>]*itemprop="actor">([^<]+)</a>`)
)

func (s *Scraper) parseListing(body []byte) []listItem {
	linkRe := s.cardLinkRe()
	titleRe := cardTitleRe

	items := make([]listItem, 0)
	seen := make(map[string]bool)
	for _, m := range linkRe.FindAllSubmatchIndex(body, -1) {
		slug := string(body[m[2]:m[3]])
		id := string(body[m[4]:m[5]])
		if seen[id] {
			continue
		}
		seen[id] = true
		it := listItem{
			id:   id,
			slug: slug,
			url:  "/" + s.cfg.VideoPath + "/" + slug + "/",
		}
		// Title lives in a title="" attribute on the same anchor tag; look
		// ahead a short window for it.
		end := m[1]
		window := body[m[0]:min(end+200, len(body))]
		if mt := titleRe.FindSubmatch(window); mt != nil {
			it.title = cleanText(string(mt[1]))
		}
		items = append(items, it)
	}
	return items
}

func (s *Scraper) parseDetail(body []byte) detailData {
	var d detailData

	if vo := parseutil.ExtractVideoObject(body); vo != nil {
		d.title = cleanText(vo.Name)
		d.description = cleanDescription(vo.Description)
		d.thumbnail = vo.ThumbnailURL
		d.preview = vo.ContentURL
		d.performers = cleanAll(vo.Actors)
		date := strings.TrimSpace(vo.UploadDate)
		if date == "" {
			date = strings.TrimSpace(vo.DatePublished)
		}
		if t, err := parseutil.TryParseDate(date, time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02"); err == nil {
			d.date = t.UTC()
		}
	}

	if m := detailDurRe.FindSubmatch(body); m != nil {
		d.duration = parseutil.ParseDurationISO(string(m[1]))
	}

	tags := []string{"Virtual Reality"}
	seen := map[string]bool{"virtual reality": true}
	for _, m := range detailTagRe.FindAllSubmatch(body, -1) {
		tag := cleanText(string(m[1]))
		key := strings.ToLower(tag)
		if tag != "" && !seen[key] {
			seen[key] = true
			tags = append(tags, tag)
		}
	}
	d.tags = tags

	// Performer fallback: if the VideoObject only carried one actor, the HTML
	// itemprop="actor" links are the authoritative full cast.
	if htmlActors := s.parseActors(body); len(htmlActors) > len(d.performers) {
		d.performers = htmlActors
	}

	return d
}

func (s *Scraper) parseActors(body []byte) []string {
	var out []string
	seen := make(map[string]bool)
	for _, m := range detailActorRe.FindAllSubmatch(body, -1) {
		name := cleanText(string(m[1]))
		key := strings.ToLower(name)
		if name != "" && !seen[key] {
			seen[key] = true
			out = append(out, name)
		}
	}
	return out
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Debugf(1, "%s: scraping %s catalogue", s.cfg.SiteID, s.cfg.ListPath)

	firstPage := true
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listURL(page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := s.parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if firstPage {
			firstPage = false
			total = s.maxPageNum(body) * len(items)
		}

		scenes := s.fetchDetails(ctx, items, opts, now)
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

// listURL builds the newest-first catalogue URL for a page. Page 1 omits the
// numeric segment.
func (s *Scraper) listURL(page int) string {
	if page <= 1 {
		return fmt.Sprintf("%s/%s?order=newest", s.base, s.cfg.ListPath)
	}
	return fmt.Sprintf("%s/%s/%d?order=newest", s.base, s.cfg.ListPath, page)
}

func (s *Scraper) maxPageNum(body []byte) int {
	maxPage := 1
	for _, m := range s.pageNumRe().FindAllSubmatch(body, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

// fetchDetails enriches each listing item from its detail page with a worker
// pool. Order is preserved so Paginate's KnownIDs early-stop fires on the
// right scene; known IDs become lightweight stubs (no detail fetch).
func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)

	results := make([]models.Scene, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		if opts.KnownIDs[it.id] {
			results[i] = models.Scene{ID: it.id, SiteID: s.cfg.SiteID}
			continue
		}
		wg.Add(1)
		go func(idx int, item listItem) {
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

			var d detailData
			if body, err := s.fetchPage(ctx, s.base+item.url); err != nil {
				scraper.Debugf(1, "%s: detail %s failed: %v (using card data)", s.cfg.SiteID, item.id, err)
			} else {
				d = s.parseDetail(body)
			}
			results[idx] = s.toScene(item, d, now)
		}(i, it)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, sc := range results {
		if sc.ID == "" {
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func (s *Scraper) toScene(it listItem, d detailData, now time.Time) models.Scene {
	title := it.title
	if d.title != "" {
		title = d.title
	}
	tags := d.tags
	if len(tags) == 0 {
		tags = []string{"Virtual Reality"}
	}
	return models.Scene{
		ID:          it.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   s.base,
		Title:       title,
		URL:         s.base + it.url,
		Studio:      s.cfg.StudioName,
		Description: d.description,
		Thumbnail:   d.thumbnail,
		Preview:     d.preview,
		Date:        d.date,
		Duration:    d.duration,
		Performers:  d.performers,
		Tags:        tags,
		ScrapedAt:   now,
	}
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

// cleanDescription unescapes the VideoObject description and strips embedded
// anchor markup the CMS injects into the prose.
func cleanDescription(s string) string {
	s = html.UnescapeString(strings.TrimSpace(s))
	s = stripTagsRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

var stripTagsRe = regexp.MustCompile(`<[^>]+>`)

func cleanAll(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]bool)
	for _, s := range in {
		c := cleanText(s)
		key := strings.ToLower(c)
		if c != "" && !seen[key] {
			seen[key] = true
			out = append(out, c)
		}
	}
	return out
}
