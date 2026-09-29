// Package blumedia scrapes the BluMedia gay-porn network — Broke Straight
// Boys (brokestraightboys.com), Boy Gusher (boygusher.com), College Boy
// Physicals (collegeboyphysicals.com) and CollegeDudes (collegedudes.com) —
// which all run the BluMedia "tour" CMS (assets on small1.blumedia.com).
//
// It is a table-driven package: one scraper is registered per site in
// init(). The public listing is paginated newest-first at
// /episodes.php?s=1&page={N} and each card links to a detail page at
// /play/{base64id}/{slug}. The scene id embedded in the URL is the
// base64 encoding of the numeric scene id (e.g. "Mzc1Mw==" -> "3753");
// the decoded numeric id is used as the stable scene ID.
//
// Detail-page markup varies per site (BSB exposes h1 + a "vid-models"
// performer block; College Boy Physicals / Boy Gusher use a "pm-tn"
// performer block + "desc" body; CollegeDudes runs an older template with
// no performer links). Parsing is therefore tolerant: each field tries
// several site selectors and falls back to the listing-card value.
package blumedia

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one BluMedia tour site served by this package.
//
// Most sites only need SiteID/Domain/StudioName: the listing path
// (/episodes.php?s=1&page=N) and detail path (/play/{id}/{slug}) are
// identical across the network.
type SiteConfig struct {
	SiteID     string // stable lowercase id, e.g. "brokestraightboys"
	Domain     string // bare domain, e.g. "brokestraightboys.com"
	StudioName string // display name, e.g. "Broke Straight Boys"
}

var sites = []SiteConfig{
	{SiteID: "brokestraightboys", Domain: "brokestraightboys.com", StudioName: "Broke Straight Boys"},
	{SiteID: "boygusher", Domain: "boygusher.com", StudioName: "Boy Gusher"},
	{SiteID: "collegeboyphysicals", Domain: "collegeboyphysicals.com", StudioName: "College Boy Physicals"},
	{SiteID: "collegedudes", Domain: "collegedudes.com", StudioName: "CollegeDudes"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newFor(cfg.SiteID))
	}
}

// newFor builds the registered scraper for a given site id. It is also used
// by the integration tests.
func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return New(cfg)
		}
	}
	return nil
}

// Scraper implements scraper.StudioScraper for a single BluMedia tour site.
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
		s.cfg.Domain + "/episodes.php?page={n}",
		s.cfg.Domain + "/play/{id}/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

var (
	// playLinkRe pulls the base64 scene id + slug from a /play/ link, plus any
	// inline link text. Hrefs use either quote style across the network's
	// templates, and a single card usually links the same scene twice (title
	// link + thumbnail link).
	playLinkRe = regexp.MustCompile(`(?s)<a [^>]*href=["']/play/([A-Za-z0-9=._-]+)/([^"'#?]+)["'][^>]*>(.*?)</a>`)
	// cardImgRe finds the thumbnail <img> (and its alt title) that follows a
	// /play/ link on the listing page.
	cardImgRe = regexp.MustCompile(`(?s)<img[^>]+src=["']([^"']+)["'][^>]*\balt=["']([^"']*)["']`)

	// episodeCardRe reads the newer "episode-card" template (CollegeBoyPhysicals'
	// episodes.php), which links player.php?vid={token} and no /play/ href.
	// Its detail page carries no per-scene data, so the card is the only source.
	episodeCardRe = regexp.MustCompile(`(?s)<div class="episode-card">.*?<a [^>]*href=["'][^"']*player\.php\?vid=([A-Za-z0-9=._-]+)["'][^>]*>(.*?)</a>.*?(?:<div class="episode-title">(.*?)</div>)?</div>`)

	// pageNumRe finds pagination page numbers for a total estimate. The
	// episode-card template drops s= and html-escapes the &, so match both.
	pageNumRe = regexp.MustCompile(`episodes\.php\?(?:s=\d+&(?:amp;)?)?page=(\d+)`)

	// Detail-page title selectors, tried in order.
	detailH1Re    = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	detailDettlRe = regexp.MustCompile(`(?s)<div class="dettl">(.*?)</div>`)
	detailTitleRe = regexp.MustCompile(`(?s)<title>(.*?)</title>`)

	// detailMetaDescRe pulls the meta description.
	detailMetaDescRe = regexp.MustCompile(`<meta name="description" content=["'](.*?)["']\s*/?>`)
	// detailDescBlockRe isolates the on-page description body ("desc" /
	// "descD" containers), preferred over the meta description when present.
	detailDescBlockRe = regexp.MustCompile(`(?s)<div class="desc[A-Za-z]*">(.*?)</div>\s*</div>`)

	// Performer selectors. BSB uses /model/{id}/{name}; College Boy Physicals
	// and Boy Gusher use /modelpage.php?id=N. Both are scoped to the
	// vid-models / pm-tn block so navigation links are not mistaken for cast.
	detailVidModelsRe = regexp.MustCompile(`(?s)<div class="vid-models">(.*?)</div>`)
	detailPmTnRe      = regexp.MustCompile(`(?s)<div class="pm-tn">(.*?)</div>`)
	modelLinkRe       = regexp.MustCompile(`(?s)<a [^>]*href=["'](?:/model/[^"']+|/modelpage\.php\?[^"']*)["'][^>]*>(.*?)</a>`)

	tagStripRe = regexp.MustCompile(`(?s)<[^>]+>`)
)

type listItem struct {
	id        string // decoded numeric id (stable scene ID)
	token     string // base64 token from the URL (used to build the play URL)
	slug      string
	title     string // listing-card title (alt / link text)
	thumbnail string
}

type detailData struct {
	title       string
	description string
	thumbnail   string
	performers  []string
}

// parseListing extracts /play/ scene links from a listing page in document
// order, deduped by decoded id. A card typically links the same scene twice
// (title link + thumbnail link); both occurrences are merged so the title can
// come from the link text and the thumbnail/alt from the image link.
func parseListing(body []byte) []listItem {
	matches := playLinkRe.FindAllSubmatch(body, -1)
	items := make([]listItem, 0, len(matches))
	byID := make(map[string]int) // id -> index into items

	for _, m := range matches {
		token := string(m[1])
		slug := string(m[2])
		inner := m[3]
		id := decodeID(token)
		if id == "" {
			continue
		}

		idx, ok := byID[id]
		if !ok {
			items = append(items, listItem{id: id, token: token, slug: slug})
			idx = len(items) - 1
			byID[id] = idx
		}
		it := &items[idx]

		// Thumbnail / alt-title from the image link.
		if mImg := cardImgRe.FindSubmatch(inner); mImg != nil {
			if it.thumbnail == "" {
				it.thumbnail = string(mImg[1])
			}
			if it.title == "" {
				it.title = cleanText(string(mImg[2]))
			}
		}
		// Plain link text (title link) as a title source.
		if it.title == "" {
			if t := cleanHTML(string(inner)); t != "" {
				it.title = t
			}
		}
	}

	// Second pass for the episode-card template. Cards already seen via a
	// /play/ link are skipped, so a page carrying both shapes yields one item
	// per scene rather than a duplicate.
	for _, m := range episodeCardRe.FindAllSubmatch(body, -1) {
		token := string(m[1])
		id := decodeID(token)
		if id == "" {
			continue
		}
		if _, ok := byID[id]; ok {
			continue
		}
		it := listItem{id: id, token: token}
		if mImg := cardImgRe.FindSubmatch(m[2]); mImg != nil {
			it.thumbnail = string(mImg[1])
			it.title = cleanText(string(mImg[2]))
		}
		if it.title == "" {
			it.title = cleanHTML(string(m[3]))
		}
		items = append(items, it)
		byID[id] = len(items) - 1
	}

	for i := range items {
		if items[i].title == "" {
			items[i].title = slugTitle(items[i].slug)
		}
	}
	return items
}

// decodeID base64-decodes the URL token to the underlying numeric scene id.
// If the token is not valid base64 (or decodes to something non-numeric) the
// token itself is returned so the scene still gets a stable id.
func decodeID(token string) string {
	dec, err := base64.StdEncoding.DecodeString(token)
	if err == nil {
		s := strings.TrimSpace(string(dec))
		if s != "" && isNumeric(s) {
			return s
		}
	}
	return token
}

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// slugTitle turns a URL slug into a human title as a last-resort fallback.
func slugTitle(slug string) string {
	words := strings.FieldsFunc(slug, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.TrimSpace(strings.Join(words, " "))
}

func parseDetail(body []byte) detailData {
	var d detailData

	d.title = firstText(body, detailH1Re, detailDettlRe)
	if d.title == "" {
		// <title> is "Scene Name | Studio" — keep the leading part only.
		if raw := firstText(body, detailTitleRe); raw != "" {
			if i := strings.IndexAny(raw, "|-"); i > 0 {
				raw = strings.TrimSpace(raw[:i])
			}
			d.title = raw
		}
	}

	// Prefer the on-page description body, fall back to the meta description.
	if blk := detailDescBlockRe.FindSubmatch(body); blk != nil {
		d.description = cleanHTML(string(blk[1]))
	}
	if d.description == "" {
		if m := detailMetaDescRe.FindSubmatch(body); m != nil {
			d.description = cleanText(string(m[1]))
		}
	}

	d.performers = parsePerformers(body)

	return d
}

// parsePerformers reads the cast from whichever performer block the site uses.
func parsePerformers(body []byte) []string {
	var block []byte
	if m := detailVidModelsRe.FindSubmatch(body); m != nil {
		block = m[1]
	} else if m := detailPmTnRe.FindSubmatch(body); m != nil {
		block = m[1]
	}
	if block == nil {
		return nil
	}

	var perfs []string
	seen := make(map[string]bool)
	for _, m := range modelLinkRe.FindAllSubmatch(block, -1) {
		name := cleanText(string(m[1]))
		if name != "" && !seen[name] {
			seen[name] = true
			perfs = append(perfs, name)
		}
	}
	return perfs
}

// firstText returns the cleaned text of the first regex that matches and
// yields non-empty content.
func firstText(body []byte, res ...*regexp.Regexp) string {
	for _, re := range res {
		if m := re.FindSubmatch(body); m != nil {
			if t := cleanHTML(string(m[1])); t != "" {
				return t
			}
		}
	}
	return ""
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Debugf(1, "%s: scraping episodes listing", s.cfg.SiteID)

	firstPage := true
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/episodes.php?s=1&page=%d", s.base, page)
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
			total = maxPageNum(body) * len(items)
		}

		scenes := s.fetchDetails(ctx, items, opts, now)
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func maxPageNum(body []byte) int {
	maxPage := 1
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if n > maxPage {
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
			if body, err := s.fetchPage(ctx, s.base+item.playURL()); err != nil {
				scraper.Debugf(1, "%s: detail %s failed: %v (using card data)", s.cfg.SiteID, item.id, err)
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

// playURL is the scene's public URL. The episode-card template has no slug, so
// it uses the player link the site publishes rather than synthesising one.
func (it listItem) playURL() string {
	if it.slug == "" {
		return "/player.php?vid=" + it.token
	}
	return "/play/" + it.token + "/" + it.slug
}

func (s *Scraper) toScene(it listItem, d detailData, now time.Time) models.Scene {
	title := it.title
	if d.title != "" {
		title = d.title
	}
	thumb := it.thumbnail
	if d.thumbnail != "" {
		thumb = d.thumbnail
	}

	return models.Scene{
		ID:          it.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   s.base,
		Title:       title,
		URL:         s.base + it.playURL(),
		Studio:      s.cfg.StudioName,
		Thumbnail:   thumb,
		Description: d.description,
		Performers:  d.performers,
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

// cleanHTML strips tags, collapses whitespace and unescapes entities — used
// for fields that may contain inline markup (titles, on-page descriptions).
func cleanHTML(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
