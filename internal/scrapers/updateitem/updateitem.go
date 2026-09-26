// Package updateitem scrapes three Elevated X tours built on the `updateItem`
// card skin: She Seduced Me and Lesbian Sexuality (both LesbianCash) and
// MySweetApple.
//
// They share the same markup vocabulary — `updateItem` cards wrapping an
// `updateDetails` block with a `tour_update_models` cast span, and detail pages
// carrying `update_title`, `latest_update_description` and `update_tags` — so
// the parsing lives here once and each site is a config row. What differs is
// only the path prefix (`/tour/` on Lesbian Sexuality, root on the other two)
// and where the date is, which the shared code handles by looking in both
// places.
//
// What the skin does NOT publish, on any of the three:
//
//   - Duration. There is no runtime anywhere, listing or detail.
//   - A scene id. Ids are derived from the thumbnail path — the numeric stem
//     under `contentthumbs/` where the site uses one, otherwise the content
//     directory name — falling back to the update slug.
//
// She Seduced Me additionally has **no date at all**: its template leaves the
// `availdate` span commented out, so the date regex is applied to the span's
// contents rather than trusting the span to hold one.
package updateitem

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	detailWorkers = 4
	// Dates are US-format throughout the skin.
	dateLayout = "01/02/2006"
)

type siteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	// Prefix is the tour root, "" or "/tour".
	Prefix string
	// Performers, when set, is the site's own resident cast. MySweetApple is a
	// couple's site whose every scene lists the brand as the model, so guests
	// only ever appear in titles.
	Performers []string
}

var sites = []siteConfig{
	{"sheseducedme", "www.sheseducedme.com", "She Seduced Me", "", nil},
	{"lesbiansexuality", "www.lesbiansexuality.com", "Lesbian Sexuality", "/tour", nil},
	{"mysweetapple", "mysweetapple.com", "MySweetApple", "", nil},
}

// Scraper implements scraper.StudioScraper for one `updateItem` site.
type Scraper struct {
	cfg     siteConfig
	Client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func newScraper(cfg siteConfig) *Scraper {
	host := strings.TrimPrefix(cfg.Domain, "www.")
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		base:    "https://" + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + strings.ReplaceAll(host, ".", `\.`) + `(?:/|$)`),
	}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newScraper(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	host := strings.TrimPrefix(s.cfg.Domain, "www.")
	return []string{
		host,
		host + s.cfg.Prefix + "/categories/movies_{N}_d.html",
		host + s.cfg.Prefix + "/updates/{slug}.html",
		host + s.cfg.Prefix + "/models/{Name}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		// `_d` is the skin's date-descending sort; `_p`, `_n`, `_o`, `_u` and
		// `_z` are other orders the KnownIDs early-stop would not be valid for.
		body, err := s.fetchPage(ctx, fmt.Sprintf("%s%s/categories/movies_%d_d.html", s.base, s.cfg.Prefix, page))
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := s.parseListing(body)
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.id] {
				seen[it.id] = true
				fresh = append(fresh, it)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)}, nil
	})
}

// ---- listing ----

var (
	cardRe = regexp.MustCompile(`<div class="updateItem"`)
	// Only the update filename is captured so the URL is rebuilt against the
	// site base — cards link absolutely.
	linkRe = regexp.MustCompile(`href="[^"]*/updates/([^"/]+\.html)"`)
	// The title is the anchor inside the details block; the card's first anchor
	// wraps the thumbnail and has no text.
	titleRe   = regexp.MustCompile(`(?s)<h4>.*?<a[^>]*>\s*(.*?)\s*</a>`)
	detailsRe = regexp.MustCompile(`(?s)<div class="updateDetails">(.*)`)
	modelsRe  = regexp.MustCompile(`(?s)<span class="tour_update_models">(.*?)</span>`)
	modelRe   = regexp.MustCompile(`/models/[^"]*\.html">([^<]+)</a>`)
	thumbRe   = regexp.MustCompile(`src0_1x="([^"]+)"`)
	// Dates are matched by shape, never by trusting a container to hold one.
	dateRe = regexp.MustCompile(`(\d{2}/\d{2}/\d{4})`)
	// Two id shapes: a numeric stem under contentthumbs/, or the content
	// directory name.
	thumbNumIDRe = regexp.MustCompile(`contentthumbs/(?:\d+/)*(\d+)-\d+x\.`)
	thumbDirIDRe = regexp.MustCompile(`content/([^/]+)/[^/]+$`)
)

type listItem struct {
	id, url, title, thumb string
	date                  time.Time
	performers            []string
}

func (s *Scraper) parseListing(body []byte) []listItem {
	page := string(body)
	starts := cardRe.FindAllStringIndex(page, -1)
	items := make([]listItem, 0, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		card := page[loc[0]:end]

		m := linkRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{url: s.base + s.cfg.Prefix + "/updates/" + m[1]}
		if tm := titleRe.FindStringSubmatch(card); tm != nil {
			it.title = cleanText(tm[1])
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = s.normalizeURL(th[1])
			it.id = sceneID(th[1])
		}
		if it.id == "" {
			it.id = strings.TrimSuffix(m[1], ".html")
		}

		if mb := modelsRe.FindStringSubmatch(card); mb != nil {
			for _, pm := range modelRe.FindAllStringSubmatch(mb[1], -1) {
				if name := cleanText(pm[1]); name != "" {
					it.performers = append(it.performers, name)
				}
			}
		}
		// Lesbian Sexuality prints the date on the card, in a bare <span> after
		// the cast. Scoping to the details block keeps thumbnail paths out.
		if db := detailsRe.FindStringSubmatch(card); db != nil {
			if d := dateRe.FindStringSubmatch(db[1]); d != nil {
				if t, err := time.Parse(dateLayout, d[1]); err == nil {
					it.date = t.UTC()
				}
			}
		}

		items = append(items, it)
	}
	return items
}

// sceneID derives a stable key from the thumbnail path. The skin exposes no id
// of its own.
func sceneID(thumb string) string {
	if m := thumbNumIDRe.FindStringSubmatch(thumb); m != nil {
		return m[1]
	}
	if m := thumbDirIDRe.FindStringSubmatch(thumb); m != nil {
		return m[1]
	}
	return ""
}

func (s *Scraper) normalizeURL(u string) string {
	switch {
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	case strings.HasPrefix(u, "/"):
		return s.base + u
	case strings.HasPrefix(u, "http"):
		return u
	default:
		return s.base + s.cfg.Prefix + "/" + u
	}
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)
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
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}
			scenes[i] = s.toScene(ctx, studioURL, it, now)
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
	updateTitleRe = regexp.MustCompile(`(?s)<span class="update_title">(.*?)</span>`)
	// The availdate span is empty on She Seduced Me — its template leaves the
	// element commented out — so the date is matched by shape inside it.
	availDateRe = regexp.MustCompile(`(?s)<span class="availdate">(.*?)</span>`)
	descRe      = regexp.MustCompile(`(?s)<span class="latest_update_description">(.*?)</span>`)
	tagsBlockRe = regexp.MustCompile(`(?s)<span class="update_tags">(.*?)</span>`)
	tagRe       = regexp.MustCompile(`/categories/[^"]*\.html">([^<]+)</a>`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         it.id,
		SiteID:     s.cfg.SiteID,
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        it.url,
		Date:       it.date,
		Thumbnail:  it.thumb,
		Performers: it.performers,
		Studio:     s.cfg.StudioName,
		ScrapedAt:  now,
	}
	if len(scene.Performers) == 0 {
		scene.Performers = s.cfg.Performers
	}

	body, err := s.fetchPage(ctx, it.url)
	if err != nil {
		// The card alone is still a usable scene.
		return scene
	}
	applyDetail(&scene, string(body))
	return scene
}

func applyDetail(scene *models.Scene, detail string) {
	if m := updateTitleRe.FindStringSubmatch(detail); m != nil {
		if title := cleanText(m[1]); title != "" {
			// The detail title is the full one; cards truncate.
			scene.Title = title
		}
	}
	if scene.Date.IsZero() {
		if m := availDateRe.FindStringSubmatch(detail); m != nil {
			if d := dateRe.FindStringSubmatch(m[1]); d != nil {
				if t, err := time.Parse(dateLayout, d[1]); err == nil {
					scene.Date = t.UTC()
				}
			}
		}
	}
	if m := descRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(tagStripRe.ReplaceAllString(m[1], " "))
	}
	if mb := tagsBlockRe.FindStringSubmatch(detail); mb != nil {
		seen := make(map[string]bool)
		for _, tm := range tagRe.FindAllStringSubmatch(mb[1], -1) {
			tag := cleanText(tm[1])
			if tag == "" || seen[tag] {
				continue
			}
			seen[tag] = true
			scene.Tags = append(scene.Tags, tag)
		}
	}
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
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
