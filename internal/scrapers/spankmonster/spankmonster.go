// Package spankmonster scrapes Spank Monster (spankmonster.com), a Ravana LLC
// white-label on the AdultEmpire "EWC" CMS.
//
// It is the same CMS family as helixstudios but a different skin — the card
// here uses `<h5 class="scene-performer-names">` and `<p class="scene-title">`
// where Helix uses an `<a class="scene-title"><h6>` and a `<p>` for performers
// — so the two do not share a parser.
//
// The site 302s every request to /AgeConfirmation without an `ageConfirmed=true`
// cookie, which every request here carries.
//
// The listing card gives id, URL, performers, thumbnail and a title prefixed
// with a rounded runtime ("51 min | Title"). Date, description, director, tags
// and the authoritative runtime live on the detail page, which a worker pool
// fetches — the card's minutes disagree with the detail page's, so the detail
// value wins.
//
// Tags are text-only: every tag anchor points at /join rather than a browsable
// tag page.
package spankmonster

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
	siteID        = "spankmonster"
	studioName    = "SpankMonster"
	detailWorkers = 4
	dateLayout    = "Jan 2, 2006"
)

var siteBase = "https://www.spankmonster.com"

// Scraper implements scraper.StudioScraper for Spank Monster.
type Scraper struct {
	Client *http.Client
}

// New constructs a Spank Monster scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"spankmonster.com",
		"spankmonster.com/spank-monster-updates.html",
		"spankmonster.com/{id}/{slug}-streaming-scene-video.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?spankmonster\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

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
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/spank-monster-updates.html?page=%d", siteBase, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
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
	// The CMS wraps attributes across lines, so every attribute pair here has
	// to tolerate arbitrary whitespace between them.
	cardRe = regexp.MustCompile(`<div class="grid-item"\s+id="ascene_(\d+)"`)
	linkRe = regexp.MustCompile(`class="scene-info-container"\s+href="(/[^"]+)"`)
	// The title is prefixed with a rounded runtime: "51 min | Real Title".
	cardTitleRe = regexp.MustCompile(`(?s)<p class="scene-title">\s*(?:\d+\s*min\s*\|\s*)?(.*?)\s*</p>`)
	cardPerfRe  = regexp.MustCompile(`(?s)<h5 class="scene-performer-names">\s*(.*?)\s*</h5>`)
	thumbRe     = regexp.MustCompile(`<img\s+src="(https://imgs1cdn\.adultempire\.com/[^"]+)"`)
)

type listItem struct {
	id, url, title, thumb string
	performers            []string
}

func parseListing(body []byte) []listItem {
	page := string(body)
	locs := cardRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]listItem, 0, len(locs))

	for i, loc := range locs {
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		card := page[loc[0]:end]

		it := listItem{id: page[loc[2]:loc[3]]}
		m := linkRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it.url = siteBase + m[1]

		if t := cardTitleRe.FindStringSubmatch(card); t != nil {
			it.title = cleanText(t[1])
		}
		if p := cardPerfRe.FindStringSubmatch(card); p != nil {
			for _, name := range strings.Split(cleanText(p[1]), ",") {
				if name = strings.TrimSpace(name); name != "" {
					it.performers = append(it.performers, name)
				}
			}
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = th[1]
		}

		items = append(items, it)
	}
	return items
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)
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
			if !scraper.Pace(ctx, delay) {
				return
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
	descRe     = regexp.MustCompile(`(?s)<h1 class="description">\s*(.*?)\s*</h1>`)
	releasedRe = regexp.MustCompile(`(?s)Released:</span>\s*([A-Z][a-z]{2} \d{1,2}, \d{4})`)
	lengthRe   = regexp.MustCompile(`(?s)Length:</span>\s*(\d+)\s*min`)
	directorRe = regexp.MustCompile(`(?s)Director:</span>\s*([^<]+?)\s*<`)
	perfRe     = regexp.MustCompile(`(?s)<div class="performer-name">\s*(.*?)\s*</div>`)
	// Tag anchors all point at /join, so they are text-only.
	tagRe = regexp.MustCompile(`data-Label="Tag"[^>]*>\s*([^<]+?)\s*</a>`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         it.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        it.url,
		Thumbnail:  it.thumb,
		Performers: it.performers,
		Studio:     studioName,
		ScrapedAt:  now,
	}

	body, err := s.fetchPage(ctx, it.url)
	if err != nil {
		return scene
	}
	applyDetail(&scene, string(body))
	return scene
}

func applyDetail(scene *models.Scene, detail string) {
	// The <h1> is the full, unprefixed title.
	if m := descRe.FindStringSubmatch(detail); m != nil {
		if t := cleanText(m[1]); t != "" {
			scene.Title = t
		}
	}
	if m := releasedRe.FindStringSubmatch(detail); m != nil {
		if ts, err := time.Parse(dateLayout, strings.TrimSpace(m[1])); err == nil {
			scene.Date = ts.UTC()
		}
	}
	// The card's minutes and the detail page's disagree; the detail page wins.
	if m := lengthRe.FindStringSubmatch(detail); m != nil {
		scene.Duration = atoi(m[1]) * 60
	}
	if m := directorRe.FindStringSubmatch(detail); m != nil {
		scene.Director = cleanText(m[1])
	}

	if names := collect(perfRe, detail); len(names) > 0 {
		scene.Performers = names
	}
	scene.Tags = collect(tagRe, detail)
}

func collect(re *regexp.Regexp, s string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		v := cleanText(m[1])
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// cleanText unescapes entities and collapses the CMS's heavy tab/newline
// padding into single spaces.
func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	// Without this the site 302s every request to /AgeConfirmation.
	headers["Cookie"] = "ageConfirmed=true"

	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: rawURL, Headers: headers})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
