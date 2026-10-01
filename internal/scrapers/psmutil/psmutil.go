// Package psmutil scrapes sites running PornSiteManager — a multi-tenant
// adult CMS hosted at gcs.pornsitemanager.com that powers the Frenchporn
// network (Citebeur, Eurocreme, Hard Kinks, AlphaMales, …) and likely other
// brands too. Every site exposes its catalog through a JSON-LD ItemList
// embedded directly in the HTML, so a clean scraper doesn't need detail-page
// fetches at all.
//
// Detection signals:
//
//   - `gcs.pornsitemanager.com` referenced for thumbs / favicons.
//   - Listing pages at `{base}/{locale}/videos` (locale is typically `en`).
//   - `<script type="application/ld+json">` containing
//     `{"@type": "ItemList", "itemListElement": [...]}` with 12 `VideoObject`
//     entries per page.
//   - Pagination via `?page=N`; past-end pages return HTTP 200 with zero items.
//   - Scene URL form: `/{locale}/videos/detail/{ID}-{slug}` (numeric ID + slug).
//
// JSON-LD VideoObject fields used:
//
//	{
//	  "url":           "https://www.citebeur.com/en/videos/detail/46311-hey-bro",
//	  "name":          "Hey bro, let's fuck him together",
//	  "thumbnailUrl":  "https://gcs.pornsitemanager.com/store/.../hd/img.jpg",
//	  "datePublished": "2026-05-27",
//	  "description":   "When Kalys arrives at the underground parking lot…",
//	  "actor": [ {"name": "Choppeur"}, {"name": "Kévin Frenchboy"}, … ]
//	}
//
// What's NOT in the JSON-LD (and skipped for now): duration, scene-specific
// tags. The HTML has duration in some sites' player markup but not uniformly;
// tags exist only as global sidebar category lists, not scene-specific.
package psmutil

import (
	"context"
	"encoding/json"
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

// SiteConfig describes one PornSiteManager-backed site.
type SiteConfig struct {
	ID       string
	SiteBase string // e.g. "https://www.citebeur.com" — no trailing slash
	Studio   string
	Locale   string // typically "en"; falls back to "en" if zero
	Patterns []string
	MatchRe  *regexp.Regexp
}

const defaultDetailWorkers = 4

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	if cfg.Locale == "" {
		cfg.Locale = "en"
	}
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string         { return s.cfg.ID }
func (s *Scraper) Patterns() []string { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool {
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- JSON-LD parsing ----

// itemList captures the ItemList JSON-LD object embedded in listing pages.
// We only need itemListElement[*].item; the wrapper exists just to anchor the
// schema.org context.
type itemList struct {
	Type  string `json:"@type"`
	Items []struct {
		Item videoObject `json:"item"`
	} `json:"itemListElement"`
}

type videoObject struct {
	Type          string        `json:"@type"`
	URL           string        `json:"url"`
	Name          string        `json:"name"`
	ThumbnailURL  string        `json:"thumbnailUrl"`
	DatePublished string        `json:"datePublished"`
	UploadDate    string        `json:"uploadDate"`
	Description   string        `json:"description"`
	Actor         []actorRef    `json:"actor"`
	Author        *organization `json:"author"`

	fromHTML bool // card parsed from markup; needs its detail page
}

type actorRef struct {
	Type string `json:"@type"`
	Name string `json:"name"`
}

type organization struct {
	Type string `json:"@type"`
	Name string `json:"name"`
}

var jsonLDRe = regexp.MustCompile(`(?s)<script type="application/ld\+json">\s*(\{.*?\})\s*</script>`)

// fixControlChars escapes raw newlines/tabs/carriage-returns that appear
// *inside* JSON strings. PornSiteManager emits human-readable JSON-LD with
// these unescaped, which is invalid per RFC 8259. We patch on the fly rather
// than crashing.
func fixControlChars(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escape := false
	for _, c := range s {
		if escape {
			b.WriteRune(c)
			escape = false
			continue
		}
		if c == '\\' {
			b.WriteRune(c)
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			b.WriteRune(c)
			continue
		}
		if inString {
			switch c {
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				b.WriteRune(c)
			}
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// parseListing extracts every VideoObject from the page's ItemList JSON-LD,
// falling back to HTML card-link extraction when no ItemList is present
// (Citebeur category pages, for example, ship only the rendered grid). The
// HTML fallback yields only URL/ID/title/thumbnail; run completes those cards
// from their detail pages (enrichFromDetail).
// Returns nil on a genuinely empty page (signal to stop pagination).
func parseListing(body []byte) ([]videoObject, error) {
	if m := jsonLDRe.FindSubmatch(body); m != nil {
		cleaned := fixControlChars(string(m[1]))
		var list itemList
		if err := json.Unmarshal([]byte(cleaned), &list); err != nil {
			return nil, fmt.Errorf("parse ItemList JSON-LD: %w", err)
		}
		if list.Type == "ItemList" {
			videos := make([]videoObject, 0, len(list.Items))
			for _, it := range list.Items {
				if it.Item.Type == "VideoObject" && it.Item.URL != "" {
					videos = append(videos, it.Item)
				}
			}
			if len(videos) > 0 {
				return videos, nil
			}
		}
		// Non-ItemList JSON-LD (BreadcrumbList on a no-results page) → fall through.
	}
	return parseListingHTML(body), nil
}

// HTML fallback regexes. A grid card links to the detail page twice — once
// around the thumbnail, once around the title:
//
//	<a href="/en/videos/detail/{ID}-{slug}">
//	  <img class="vc-img" alt="{image filename}" src="https://{cdn}/store/…/sd/{thumb}.jpg">
//	</a>
//	<a class="… fw-bold" href="/en/videos/detail/{ID}-{slug}">{title}</a>
//
// The image alt is the uploaded file's name, not the scene title, so it is
// only a last resort. The CDN host has changed before (gcs.pornsitemanager.com
// → images.gayvideo.network), so it is not pinned.
var (
	htmlCardLinkRe  = regexp.MustCompile(`href="(/[a-z]{2,3}/videos/detail/\d+-[a-z0-9-]+)"`)
	htmlCardImgRe   = regexp.MustCompile(`<img[^>]+alt="([^"]*)"[^>]+src="(https?://[^"]+)"`)
	htmlCardTitleRe = regexp.MustCompile(`href="/[a-z]{2,3}/videos/detail/[^"]+"[^>]*>\s*([^<]*[^<\s])\s*</a>`)
)

func parseListingHTML(body []byte) []videoObject {
	s := string(body)
	// A card spans from the first link to its detail URL up to the first link
	// to a different one, so both anchors of a card land in the same block and
	// a stray <img> elsewhere on the page cannot pollute the wrong card.
	matches := htmlCardLinkRe.FindAllStringSubmatchIndex(s, -1)
	out := make([]videoObject, 0, len(matches))
	seen := map[string]bool{}
	for i, loc := range matches {
		url := s[loc[2]:loc[3]]
		if seen[url] {
			continue
		}
		seen[url] = true

		end := len(s)
		for _, next := range matches[i+1:] {
			if s[next[2]:next[3]] != url {
				end = next[0]
				break
			}
		}
		block := s[loc[0]:end]

		v := videoObject{Type: "VideoObject", URL: url, fromHTML: true}
		if m := htmlCardImgRe.FindStringSubmatch(block); m != nil {
			v.Name = m[1]
			v.ThumbnailURL = m[2]
		}
		if m := htmlCardTitleRe.FindStringSubmatch(block); m != nil {
			v.Name = strings.TrimSpace(m[1])
		}
		out = append(out, v)
	}
	return out
}

// parseDetail returns the VideoObject JSON-LD of a scene detail page.
func parseDetail(body []byte) (videoObject, bool) {
	for _, m := range jsonLDRe.FindAllSubmatch(body, -1) {
		var v videoObject
		if err := json.Unmarshal([]byte(fixControlChars(string(m[1]))), &v); err != nil {
			continue
		}
		if v.Type == "VideoObject" {
			return v, true
		}
	}
	return videoObject{}, false
}

// merge overlays the detail page's fields onto a card scraped from HTML.
func (v videoObject) merge(d videoObject) videoObject {
	if d.Name != "" {
		v.Name = d.Name
	}
	if d.ThumbnailURL != "" {
		v.ThumbnailURL = d.ThumbnailURL
	}
	if d.DatePublished != "" {
		v.DatePublished = d.DatePublished
	}
	if d.UploadDate != "" {
		v.UploadDate = d.UploadDate
	}
	if d.Description != "" {
		v.Description = d.Description
	}
	if len(d.Actor) > 0 {
		v.Actor = d.Actor
	}
	return v
}

// ---- URL handling ----

// sceneIDRe extracts the numeric scene ID from `/{locale}/videos/detail/{ID}-{slug}`.
// Some PSM sites use non-numeric slug-only IDs (`/detail/kevin-frenchboy`); for
// those we fall back to the full slug.
var sceneIDRe = regexp.MustCompile(`/videos/detail/(\d+)-`)

func extractSceneID(url string) string {
	if m := sceneIDRe.FindStringSubmatch(url); m != nil {
		return m[1]
	}
	// Fallback: last path segment, with .html / query / fragment stripped.
	if i := strings.LastIndex(url, "/detail/"); i >= 0 {
		rest := url[i+len("/detail/"):]
		rest = strings.SplitN(rest, "?", 2)[0]
		rest = strings.SplitN(rest, "#", 2)[0]
		rest = strings.TrimSuffix(rest, ".html")
		return rest
	}
	return url
}

// listingURL builds `{base}/{locale}/videos?page=N`. Category-mode URLs use
// `{base}/{locale}/videos/{slug}?page=N` (see categoryFromURL).
func (s *Scraper) listingURL(category string, page int) string {
	path := "videos"
	if category != "" {
		path = "videos/" + category
	}
	if page <= 1 {
		return fmt.Sprintf("%s/%s/%s", s.cfg.SiteBase, s.cfg.Locale, path)
	}
	return fmt.Sprintf("%s/%s/%s?page=%d", s.cfg.SiteBase, s.cfg.Locale, path, page)
}

// categoryFromURL returns the category slug if studioURL points at a tag/
// category listing like `/en/videos/{slug}`, else empty (= full catalog).
var categoryRe = regexp.MustCompile(`/[a-z]{2,3}/videos/([a-z0-9-]+)(?:[/?]|$)`)

func categoryFromURL(studioURL string) string {
	if m := categoryRe.FindStringSubmatch(studioURL); m != nil {
		return m[1]
	}
	return ""
}

// ---- main scrape loop ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	category := categoryFromURL(studioURL)
	if category != "" {
		scraper.Debugf(1, "%s: scraping category %q", s.cfg.ID, category)
	} else {
		scraper.Debugf(1, "%s: scraping full catalog", s.cfg.ID)
	}

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(category, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		videos, err := parseListing(body)
		if err != nil {
			return scraper.PageResult{}, err
		}
		s.enrichFromDetail(ctx, videos, opts, out)
		scenes := make([]models.Scene, len(videos))
		for i, v := range videos {
			scenes[i] = v.toScene(s.cfg.ID, s.cfg.SiteBase, s.cfg.Studio, now)
		}
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func (v videoObject) toScene(siteID, siteBase, studio string, now time.Time) models.Scene {
	url := v.URL
	if strings.HasPrefix(url, "/") {
		url = siteBase + url
	}

	scene := models.Scene{
		ID:          extractSceneID(v.URL),
		SiteID:      siteID,
		StudioURL:   siteBase,
		Title:       html.UnescapeString(v.Name),
		URL:         url,
		Thumbnail:   v.ThumbnailURL,
		Description: html.UnescapeString(v.Description),
		Studio:      studio,
		ScrapedAt:   now,
	}

	// Date: prefer the more-precise uploadDate (RFC3339-ish), fall back to
	// datePublished (YYYY-MM-DD).
	if v.UploadDate != "" {
		if t, err := time.Parse(time.RFC3339, v.UploadDate); err == nil {
			scene.Date = t.UTC()
		}
	}
	if scene.Date.IsZero() && v.DatePublished != "" {
		if t, err := time.Parse("2006-01-02", v.DatePublished); err == nil {
			scene.Date = t.UTC()
		}
	}

	for _, a := range v.Actor {
		name := strings.TrimSpace(html.UnescapeString(a.Name))
		if name != "" {
			scene.Performers = append(scene.Performers, name)
		}
	}

	return scene
}

// enrichFromDetail completes cards that came from the HTML fallback: those
// listings (category pages) carry no date, description or performers, so each
// unknown scene's detail page is fetched for its VideoObject JSON-LD. A failed
// fetch is reported and the card kept with what the listing gave.
func (s *Scraper) enrichFromDetail(ctx context.Context, videos []videoObject, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	var jobs []int
	for i, v := range videos {
		if v.fromHTML && !opts.KnownIDs[extractSceneID(v.URL)] {
			jobs = append(jobs, i)
		}
	}
	if len(jobs) == 0 {
		return
	}
	workers := min(scraper.WorkerCount(opts, defaultDetailWorkers), len(jobs))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.ID, len(jobs), workers)

	jobCh := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobCh {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				u := videos[i].URL
				if strings.HasPrefix(u, "/") {
					u = s.cfg.SiteBase + u
				}
				body, err := s.fetchPage(ctx, u)
				if err == nil {
					if d, ok := parseDetail(body); ok {
						videos[i] = videos[i].merge(d)
						continue
					}
					err = scraper.ParseError(u, fmt.Errorf("no VideoObject JSON-LD"))
				}
				select {
				case out <- scraper.Error(fmt.Errorf("detail %s: %w", u, err)):
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	for _, i := range jobs {
		select {
		case jobCh <- i:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobCh)
	wg.Wait()
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
