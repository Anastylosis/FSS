// Package sindrive scrapes SinDrive and its sister sites running on the
// Thumbmaxx (asset1.thumbmaxx.com) clip-shop CMS. The same catalogue is
// served by three domains:
//
//   - sindrive.com   — SinDrive flagship branding
//   - sinx.com       — SINX umbrella shop, hosts the per-series "channels"
//     (Backstage-Bangers, Bikini-Beach-Balls, etc.)
//   - madsexparty.com — Mad Sex Party branding, identical catalogue
//
// All three return byte-identical responses for `/videos/all` and share the
// same `/{Channel-Name}/movie/{id}/{slug}` detail-URL scheme. Scene detail
// pages are paywalled (every detail link redirects through `/login?return=…`)
// so all metadata has to come from the listing cards.
//
// Supported entry URLs:
//
//	https://www.sindrive.com/                  → /videos/all
//	https://www.sindrive.com/videos/all        → main "Latest Deals" listing
//	https://www.sinx.com/Backstage-Bangers     → channel listing
//	https://www.sinx.com/channel/X/all         → legacy form, rewritten to /X
//	https://www.madsexparty.com/videos/all     → same catalogue, alt branding
//
// Card markup (one item):
//
//	<figure class=" video_item " ...>
//	  <a href="/{Channel}/movie/{id}/{slug}" class="let__up-link">
//	    <img class="thumb-slide" src="https://1896493691.rsc.cdn77.org/.../510x290/...jpg" ...>
//	  </a>
//	  <div class="video_item--content with-badge">
//	    <a href="/{Channel}/movie/{id}/{slug}" class="link" title="Full Title">
//	      <h3 class="title--5">Display Title</h3>
//	    </a>
//	  </div>
//	  <div class="video_item--channel-link">
//	    <a href="/{Channel}" class="link">Channel Display Name</a>
//	  </div>
//	</figure>
//
// Fields lifted from the card: ID (from /movie/{id}/), title, channel, thumb.
// Date and duration are not present in the listing markup; left zero/empty.
//
// Pagination: `?page=N` query string. `<ul class="pagination">` on the first
// page contains a `class="last-page"` <li> pointing at the highest page,
// which we use to estimate the total scene count. Past-end pages return a
// listing with zero `<figure class=" video_item ">` matches (clean stop
// signal).
package sindrive

import (
	"context"
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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	studioName = "SinDrive"
	scraperID  = "sindrive"
)

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return scraperID }

func (s *Scraper) Patterns() []string {
	return []string{
		"sindrive.com/",
		"sindrive.com/videos/all",
		"sinx.com/",
		"sinx.com/videos/all",
		"sinx.com/{Channel-Name}",
		"sinx.com/channel/{Channel-Name}/all",
		"sinx.com/girls/{id}-{slug}",
		"sinx.com/tag/{id}-{slug}",
		"madsexparty.com/",
		"madsexparty.com/videos/all",
	}
}

var matchRe = regexp.MustCompile(
	`^https?://(?:www\.)?(?:sindrive\.com|sinx\.com|madsexparty\.com)(?:/|$)`,
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// resolveListingPath normalises whatever URL the user provides to the actual
// listing path we should fetch.
//
//   - "/" / empty path             → "/videos/all" (the public catalogue)
//   - "/channel/{Name}/all"        → "/{Name}"     (legacy form, the bare
//     channel URL is the
//     canonical listing)
//   - "/{Name}", "/videos/...", …  → passed through unchanged
//
// A detail-page URL (`/{Channel}/movie/{id}/…`) is rejected — those are
// paywalled and have no listing semantics.
func resolveListingPath(rawPath string) (string, error) {
	p := strings.TrimSpace(rawPath)
	if p == "" || p == "/" {
		return "/videos/all", nil
	}
	// /channel/X/all → /X
	if m := legacyChannelRe.FindStringSubmatch(p); m != nil {
		return "/" + m[1], nil
	}
	// Reject detail URLs.
	if detailPathRe.MatchString(p) {
		return "", fmt.Errorf("sindrive: %q looks like a paywalled detail page, not a listing", p)
	}
	return p, nil
}

var (
	// /channel/Backstage-Bangers/all → captures "Backstage-Bangers".
	legacyChannelRe = regexp.MustCompile(`^/channel/([A-Za-z0-9][A-Za-z0-9-]*)/all/?$`)
	// /{Channel}/movie/{id}/{slug} → detail page (paywalled).
	detailPathRe = regexp.MustCompile(`^/[A-Za-z0-9][A-Za-z0-9-]*/movie/\d+/`)

	// Card parsing.
	cardStartRe  = regexp.MustCompile(`<figure class=" video_item "`)
	detailHrefRe = regexp.MustCompile(`href="(/[A-Za-z0-9][A-Za-z0-9-]*/movie/(\d+)/[a-z0-9-]+)"`)
	titleRe      = regexp.MustCompile(`(?s)<h3 class="title--5"[^>]*>\s*(.*?)\s*</h3>`)
	// Channel link block — the channel anchor sits after the marker div, but
	// there's a sibling `<div class="nc-icon-mini …"></div>` in between, so we
	// can't just slice `<div class="video_item--channel-link">…</div>` with a
	// lazy `.*?`. Find the marker, then look for the next `<a href="/Name">…</a>`
	// whose target is a single path segment (no slashes, query, or `#`).
	channelMarkerRe = regexp.MustCompile(`<div class="video_item--channel-link">`)
	channelAnchorRe = regexp.MustCompile(`(?s)<a[^>]+href="/([A-Za-z0-9][A-Za-z0-9-]*)"[^>]*>\s*([^<]*?)\s*</a>`)
	// Thumbnail — listing uses lazy-loaded `<img class="thumb-slide" src="…">`.
	thumbRe = regexp.MustCompile(`<img[^>]+class="thumb-slide"[^>]+src="([^"]+)"`)
	// Pagination max-page lookup. The `last-page` <li> wraps an <a> whose href
	// contains `page=N`.
	lastPageRe = regexp.MustCompile(`<li[^>]+class="last-page"[^>]*><a[^>]+href="[^"]*[?&]page=(\d+)`)
)

type sceneItem struct {
	id     string
	url    string // path-only, e.g. "/Channel/movie/123/slug"
	title  string
	series string // the "channel" anchor on the card — actually a per-product series name
	thumb  string
}

func parseListing(body []byte) []sceneItem {
	page := string(body)
	starts := cardStartRe.FindAllStringIndex(page, -1)
	items := make([]sceneItem, 0, len(starts))
	seen := make(map[string]bool, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[loc[0]:end]

		var item sceneItem
		// ID + URL come from the first /movie/ link inside the card.
		if m := detailHrefRe.FindStringSubmatch(block); m != nil {
			item.url = m[1]
			item.id = m[2]
		}
		if item.id == "" || seen[item.id] {
			continue
		}
		seen[item.id] = true

		if m := titleRe.FindStringSubmatch(block); m != nil {
			item.title = html.UnescapeString(strings.TrimSpace(m[1]))
		}
		if loc := channelMarkerRe.FindStringIndex(block); loc != nil {
			if ca := channelAnchorRe.FindStringSubmatch(block[loc[1]:]); ca != nil {
				item.series = html.UnescapeString(strings.TrimSpace(ca[2]))
			}
		}
		if m := thumbRe.FindStringSubmatch(block); m != nil {
			item.thumb = m[1]
		}

		items = append(items, item)
	}
	return items
}

// estimateTotal reads the "last-page" <li> from the pagination block and
// multiplies by perPage. Returns 0 when no last-page marker is present
// (single-page listing); the channel-link UI omits the last-page <li> on the
// final page.
func estimateTotal(body []byte, perPage int) int {
	m := lastPageRe.FindSubmatch(body)
	if m == nil {
		return perPage
	}
	n, _ := strconv.Atoi(string(m[1]))
	if n < 1 {
		return perPage
	}
	return n * perPage
}

// listingURL appends ?page=N (or &page=N) onto the base listing path, parsing
// the supplied entry URL so that any user-provided query string (e.g.
// `?sort=newest`) is preserved.
func (s *Scraper) listingURL(entryURL string, path string, page int) (string, error) {
	parsed, err := url.Parse(entryURL)
	if err != nil {
		return "", err
	}
	parsed.Path = path
	q := parsed.Query()
	q.Set("page", strconv.Itoa(page))
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	parsed, err := url.Parse(studioURL)
	if err != nil || parsed.Host == "" {
		select {
		case out <- scraper.Error(fmt.Errorf("sindrive: invalid URL %q: %w", studioURL, err)):
		case <-ctx.Done():
		}
		return
	}
	listingPath, err := resolveListingPath(parsed.Path)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "sindrive: scraping %s%s", parsed.Host, listingPath)

	now := time.Now().UTC()
	base := parsed.Scheme + "://" + parsed.Host
	scraper.Paginate(ctx, opts, scraperID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL, err := s.listingURL(studioURL, listingPath, page)
		if err != nil {
			return scraper.PageResult{}, fmt.Errorf("build URL: %w", err)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			total = estimateTotal(body, len(items))
		}

		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = item.toScene(base, studioURL, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func (item sceneItem) toScene(siteBase, studioURL string, now time.Time) models.Scene {
	full := item.url
	if strings.HasPrefix(full, "/") {
		full = siteBase + full
	}
	return models.Scene{
		ID:        item.id,
		SiteID:    scraperID,
		StudioURL: studioURL,
		Title:     item.title,
		URL:       full,
		Thumbnail: item.thumb,
		Studio:    studioName,
		Series:    item.series,
		ScrapedAt: now,
	}
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
