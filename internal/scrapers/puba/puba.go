// Package puba scrapes the Puba pornstar network at puba.com. The site
// has a JSON-API page at `/pornstarnetwork/index.php` that takes a few
// well-known query parameters:
//
//   - `section=538` — videos (vs. 539 = sites listing, 320 = images)
//   - `view=v` — videos view (when not filtering by `group`)
//   - `group={N}` — per-pornstar / per-sub-site filter
//   - `searching=Search` — required: the PHP backend treats the request
//     as a search request only when this flag is present
//   - `start={offset}&count={N}` — pagination
//   - `format=json&resource=video` — switch from HTML to JSON
//
// JSON response shape: `{total, page, num_pages, items:[{galid, secid,
// description, image_url, video_url, actors, time, favorite}]}`.
//
// Items are sorted by `galid` descending (newest first), which makes
// incremental scrapes with `KnownIDs` early-stop reliable.
//
// 13 pornstar sub-sites map to `group={N}` filter IDs; the network's
// `?section=539` index lists them all as `<!-- {SiteName} --> … group=N`
// pairs. The parent `puba` scraper omits the filter and walks the full
// 2800+ video catalogue.
package puba

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	baseURL    = "https://www.puba.com/pornstarnetwork"
	studioName = "Puba"
	section    = 538 // videos section
	perPage    = 24
)

// SiteConfig describes one Puba scraper — either the parent network
// (Group == 0) or a per-pornstar / per-sub-site filter (Group > 0).
type SiteConfig struct {
	ID       string
	SiteName string // shown in Scene.Series; empty for the parent network
	Group    int    // 0 = whole catalogue, N = pornstarnetwork group filter
	Patterns []string
	MatchRe  *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
	// base is the site root used for *fetches*, a field so offline tests can
	// redirect them. Output fields (Scene.URL, Thumbnail) keep the const: a
	// scene's public address must not become a 127.0.0.1 URL because a test
	// happened to fetch it from there.
	base string
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
		base:   baseURL,
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

// ---- JSON shape ----

type apiResponse struct {
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	NumPages int       `json:"num_pages"`
	Start    int       `json:"start"`
	Count    int       `json:"count"`
	Items    []apiItem `json:"items"`
}

type apiItem struct {
	GalID       int    `json:"galid"`
	SecID       int    `json:"secid"`
	Description string `json:"description"`
	VideoURL    string `json:"video_url"` // relative: show_video.php?galid=…&nats=…
	ImageURL    string `json:"image_url"` // relative: view_image.php?gal=…&file=sample.jpg
	Actors      string `json:"actors"`    // HTML: "<a href='…'>Name</a>, …"
	Time        string `json:"time"`      // "MM:SS" or "HH:MM:SS"
	Favorite    bool   `json:"favorite"`
}

// ---- listing fetch ----

func (s *Scraper) listingURL(start int) string {
	q := url.Values{}
	q.Set("section", strconv.Itoa(section))
	if s.cfg.Group == 0 {
		q.Set("view", "v") // whole-network video view
	} else {
		q.Set("group", strconv.Itoa(s.cfg.Group))
	}
	q.Set("searching", "Search")
	q.Set("start", strconv.Itoa(start))
	q.Set("count", strconv.Itoa(perPage))
	q.Set("format", "json")
	q.Set("resource", "video")
	return s.base + "/index.php?" + q.Encode()
}

func (s *Scraper) fetchPage(ctx context.Context, start int) (*apiResponse, error) {
	u := s.listingURL(start)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: u,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
			"Accept":     "application/json, text/javascript, */*; q=0.01",
			"Referer":    s.base + "/index.php?section=" + strconv.Itoa(section),
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	// The PHP response prepends a lot of whitespace + the occasional
	// HTML comment before the JSON payload. Trim everything up to the
	// first '{' to make json.Unmarshal happy.
	body = trimLeadingNoise(body)

	var data apiResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("decode listing: %w", err)
	}
	return &data, nil
}

// trimLeadingNoise drops any leading whitespace/HTML before the first
// `{` so the JSON decoder sees only the API payload. The server emits
// the JSON body inside a PHP template that includes a bunch of layout
// fragments before the real response.
func trimLeadingNoise(body []byte) []byte {
	for i, b := range body {
		if b == '{' {
			return body[i:]
		}
	}
	return body
}

// ---- run loop ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.Debugf(1, "puba/%s: starting (group=%d)", s.cfg.ID, s.cfg.Group)

	now := time.Now().UTC()
	sentTotal := false

	for start := 0; ; start += perPage {
		if ctx.Err() != nil {
			return
		}
		if start > 0 && !scraper.Pace(ctx, opts.Delay) {
			return
		}

		scraper.Debugf(1, "puba/%s: fetching start=%d", s.cfg.ID, start)
		page, err := s.fetchPage(ctx, start)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("start=%d: %w", start, err)):
			case <-ctx.Done():
			}
			return
		}
		if !sentTotal {
			scraper.Debugf(1, "puba/%s: %d total scenes", s.cfg.ID, page.Total)
			if page.Total > 0 {
				select {
				case out <- scraper.Progress(page.Total):
				case <-ctx.Done():
					return
				}
			}
			sentTotal = true
		}
		if len(page.Items) == 0 {
			return
		}

		for _, it := range page.Items {
			id := strconv.Itoa(it.GalID)
			if opts.KnownIDs[id] {
				scraper.Debugf(1, "puba/%s: hit known ID %s, stopping early", s.cfg.ID, id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case out <- scraper.Scene(s.toScene(it, studioURL, now)):
			case <-ctx.Done():
				return
			}
		}

		// Stop when we've covered every page reported by the API. The
		// per-group listings can be short (e.g. ~67 items for Samantha
		// Saint = 3 pages) so we rely on the server's num_pages rather
		// than waiting for an empty page.
		if page.NumPages > 0 && page.Page >= page.NumPages {
			return
		}
	}
}

// ---- Scene materialisation ----

var (
	// actorLinkRe pulls the inner text of every `<a>` tag in the
	// `actors` field — that's the performer name. The link wraps
	// `index.php?section=538&actor=N&nats=…`.
	actorLinkRe = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	// reservedQS drops the `&nats=` query parameter (a tracking code
	// rotated per request) from the relative `video_url` / `image_url`
	// values so resulting absolute URLs are stable across scrapes.
	natsQSRe = regexp.MustCompile(`[?&]nats=[^&]*`)
)

func (s *Scraper) toScene(it apiItem, studioURL string, now time.Time) models.Scene {
	studio := studioName
	series := s.cfg.SiteName

	scene := models.Scene{
		ID:        strconv.Itoa(it.GalID),
		SiteID:    s.cfg.ID,
		StudioURL: studioURL,
		Title:     cleanText(it.Description),
		URL:       absURL(it.VideoURL),
		Thumbnail: absURL(it.ImageURL),
		Studio:    studio,
		Series:    series,
		ScrapedAt: now,
	}

	if perf := parsePerformers(it.Actors); len(perf) > 0 {
		scene.Performers = perf
	}
	if secs := parseutil.ParseDurationColon(strings.TrimSpace(it.Time)); secs > 0 {
		scene.Duration = secs
	}

	return scene
}

func parsePerformers(actorsHTML string) []string {
	if actorsHTML == "" {
		return nil
	}
	matches := actorLinkRe.FindAllStringSubmatch(actorsHTML, -1)
	out := make([]string, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		name := cleanText(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func absURL(rel string) string {
	if rel == "" {
		return ""
	}
	clean := natsQSRe.ReplaceAllString(rel, "")
	// After stripping `&nats=…`, fix any orphan trailing `?` / `&`.
	clean = strings.TrimRight(clean, "?&")
	if strings.HasPrefix(clean, "http") {
		return clean
	}
	return baseURL + "/" + strings.TrimPrefix(clean, "/")
}

var wsRe = regexp.MustCompile(`\s+`)

func cleanText(s string) string {
	if s == "" {
		return ""
	}
	s = html.UnescapeString(s)
	s = wsRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
