// Package loveherfilms scrapes the Love Her Films network — Love Her Feet
// (loveherfeet.com), Love Her Boobs (loveherboobs.com), Love Her Butt
// (loveherbutt.com) and She Loves Black (shelovesblack.com), plus the network
// hub Love Her Films (loveherfilms.com). All sites run the same Next.js /
// ElevatedX "oktogon" tour CMS. It is a table-driven package: one scraper is
// registered per site in init().
//
// The public movie listing is paginated newest-first at
// /tour/categories/movies/{N}/latest/. Each listing page embeds the scene
// records in the Next.js RSC payload (groupId-anchored content sets) and, when
// server-rendered, also as HTML cards. Detail pages live at
// /tour/trailers/{slug}/ and always carry a schema.org VideoObject JSON-LD
// block plus a "Featuring:" performer list and a "Tags:" category list.
//
// Listing slugs are read from the embedded payload (always present, regardless
// of the intermittent page-1 SSR caching) and each scene is enriched from its
// detail page with a worker pool.
package loveherfilms

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

// pageSize is the number of scenes the CMS returns per movie listing page.
const pageSize = 12

// SiteConfig describes one Love Her Films tour site served by this package.
type SiteConfig struct {
	SiteID     string // stable lowercase id, e.g. "loveherfeet"
	Domain     string // bare domain, e.g. "loveherfeet.com"
	StudioName string // display name, e.g. "Love Her Feet"
}

var sites = []SiteConfig{
	{SiteID: "loveherfeet", Domain: "loveherfeet.com", StudioName: "Love Her Feet"},
	{SiteID: "loveherboobs", Domain: "loveherboobs.com", StudioName: "Love Her Boobs"},
	{SiteID: "loveherbutt", Domain: "loveherbutt.com", StudioName: "Love Her Butt"},
	{SiteID: "shelovesblack", Domain: "shelovesblack.com", StudioName: "She Loves Black"},
	{SiteID: "loveherfilms", Domain: "loveherfilms.com", StudioName: "Love Her Films"},
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

// Scraper implements scraper.StudioScraper for a single Love Her Films site.
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
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + escaped + `(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/tour/categories/movies/{n}/latest/",
		s.cfg.Domain + "/tour/trailers/{slug}/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

const listPathTemplate = "/tour/categories/movies/%d/latest/"

var (
	// payloadItemRe matches one content-set record head in the embedded
	// Next.js RSC payload. Records are escaped (\"key\":\"value\"). The
	// groupId,title,slug,type adjacency uniquely identifies a scene (category
	// records lack groupId), so this reliably yields scene slugs in newest-first
	// order regardless of whether the page server-rendered its HTML cards.
	payloadItemRe = regexp.MustCompile(`\\"groupId\\":\\"[0-9a-f]+\\",\\"title\\":\\"((?:[^"\\]|\\.)*?)\\",\\"slug\\":\\"([a-zA-Z0-9][^"\\]*?)\\",\\"type\\":`)

	// countRe pulls the total scene count from the listing query payload.
	countRe = regexp.MustCompile(`\\"count\\":(\d+),\\"items\\":\[`)

	// detailFeatBlockRe isolates the "Featuring:" performer list on a detail
	// page; detailModelAltRe pulls each performer name from a model link's img
	// alt within that block.
	detailFeatBlockRe = regexp.MustCompile(`(?s)Featuring:</h2>.*?</ul>`)
	detailModelAltRe  = regexp.MustCompile(`(?s)href="/tour/models/[^"]+"[^>]*>.*?alt="([^"]+)"`)

	// detailTagBlockRe isolates the "Tags:" category list; detailTagRe pulls
	// each tag's display text.
	detailTagBlockRe = regexp.MustCompile(`(?s)Tags:</h2>.*?</ul>`)
	detailTagRe      = regexp.MustCompile(`(?s)href="/tour/categories/[^"]+"[^>]*>.*?>([^<]+)</p>`)

	// detailDurRe matches the visible HH:MM:SS runtime in the player area.
	detailDurRe = regexp.MustCompile(`>(\d{1,2}:\d{2}:\d{2})<`)
)

type listItem struct {
	id    string // trailer slug
	title string
}

type detailData struct {
	description string
	thumbnail   string
	preview     string
	date        time.Time
	duration    int
	performers  []string
	tags        []string
}

// parseListing extracts the page's scene slugs (with payload titles) in
// newest-first document order, capped at pageSize so trailing "suggested"
// content sets embedded in the same payload are not mistaken for page items.
func parseListing(body []byte) []listItem {
	seen := make(map[string]bool)
	items := make([]listItem, 0, pageSize)
	for _, m := range payloadItemRe.FindAllSubmatch(body, -1) {
		title := html.UnescapeString(strings.TrimSpace(unescapeJSON(string(m[1]))))
		slug := string(m[2])
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		items = append(items, listItem{id: slug, title: title})
		if len(items) >= pageSize {
			break
		}
	}
	return items
}

// listingTotal returns the total scene count advertised by the listing payload.
func listingTotal(body []byte) int {
	if m := countRe.FindSubmatch(body); m != nil {
		if n, err := strconv.Atoi(string(m[1])); err == nil {
			return n
		}
	}
	return 0
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Debugf(1, "%s: scraping movie listing", s.cfg.SiteID)

	firstPage := true
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + fmt.Sprintf(listPathTemplate, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if firstPage {
			firstPage = false
			total = listingTotal(body)
		}

		scenes := s.fetchDetails(ctx, items, opts, now)
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   len(items) < pageSize,
		}, nil
	})
}

// fetchDetails enriches each listing item from its detail page with a worker
// pool. Order is preserved so Paginate's KnownIDs early-stop fires on the right
// scene; known IDs become lightweight stubs (no detail fetch).
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
			if body, err := s.fetchPage(ctx, s.detailURL(item.id)); err != nil {
				scraper.Debugf(1, "%s: detail %s failed: %v (using listing data)", s.cfg.SiteID, item.id, err)
			} else {
				d = parseDetail(body)
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

func (s *Scraper) detailURL(slug string) string {
	return s.base + "/tour/trailers/" + slug + "/"
}

func parseDetail(body []byte) detailData {
	var d detailData

	if vo := parseutil.ExtractVideoObject(body); vo != nil {
		d.description = cleanText(vo.Description)
		d.thumbnail = strings.TrimSpace(vo.ThumbnailURL)
		d.preview = strings.TrimSpace(vo.ContentURL)
		date := strings.TrimSpace(vo.UploadDate)
		if date == "" {
			date = strings.TrimSpace(vo.DatePublished)
		}
		if t, err := parseutil.TryParseDate(date,
			"2006-01-02T15:04:05.000Z07:00",
			time.RFC3339,
			"2006-01-02T15:04:05Z07:00",
			"2006-01-02",
		); err == nil {
			d.date = t.UTC()
		}
	}

	if blk := detailFeatBlockRe.Find(body); blk != nil {
		seen := make(map[string]bool)
		for _, m := range detailModelAltRe.FindAllSubmatch(blk, -1) {
			name := cleanText(string(m[1]))
			if name != "" && !seen[name] {
				seen[name] = true
				d.performers = append(d.performers, name)
			}
		}
	}

	if blk := detailTagBlockRe.Find(body); blk != nil {
		seen := make(map[string]bool)
		for _, m := range detailTagRe.FindAllSubmatch(blk, -1) {
			tag := cleanText(string(m[1]))
			if tag != "" && !seen[tag] {
				seen[tag] = true
				d.tags = append(d.tags, tag)
			}
		}
	}

	if m := detailDurRe.FindSubmatch(body); m != nil {
		d.duration = parseutil.ParseDurationColon(string(m[1]))
	}

	return d
}

func (s *Scraper) toScene(it listItem, d detailData, now time.Time) models.Scene {
	return models.Scene{
		ID:          it.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   s.base,
		Title:       it.title,
		URL:         s.detailURL(it.id),
		Studio:      s.cfg.StudioName,
		Description: d.description,
		Thumbnail:   d.thumbnail,
		Preview:     d.preview,
		Date:        d.date,
		Duration:    d.duration,
		Performers:  d.performers,
		Tags:        d.tags,
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

// unescapeJSON reverses the backslash escaping the Next.js RSC payload applies
// to embedded JSON strings (\" -> ", \\ -> \, \/ -> /).
func unescapeJSON(s string) string {
	r := strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\/`, `/`)
	return r.Replace(s)
}
