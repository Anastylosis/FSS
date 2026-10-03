// Package concoxxxion scrapes Conor Coxxx (conorcoxxx.com) and Horny Household
// (hornyhousehold.com), Concoxxxion's two AdultEmpire "EWC" white-label sites.
//
// Both sites share one scene catalogue (same IDs on either domain) and the
// same listing skin, which differs from spankmonster and helixstudios.
package concoxxxion

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "concoxxxion"
	defaultStudio = "Concoxxxion"
	detailWorkers = 4
	dateLayout    = "Jan 2, 2006"
	newestPath    = "/watch-newest-clips-and-scenes.html"
)

// siteBase overrides the origin taken from the studio URL (tests only).
var siteBase string

// Scraper implements scraper.StudioScraper for Concoxxxion.
type Scraper struct {
	Client *http.Client
}

// New constructs a Concoxxxion scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"hornyhousehold.com",
		"hornyhousehold.com/watch-newest-clips-and-scenes.html",
		"conorcoxxx.com",
		"conorcoxxx.com/conor-coxxx-clips.html",
		"conorcoxxx.com/horny-household-clips.html",
		"conorcoxxx.com/watch-newest-clips-and-scenes.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:hornyhousehold|conorcoxxx)\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

// listingFor returns the origin and listing path for a studio URL.
func listingFor(studioURL string) (base, path string) {
	u, err := url.Parse(studioURL)
	if err != nil || u.Host == "" {
		return "https://hornyhousehold.com", newestPath
	}
	base = u.Scheme + "://" + u.Host
	if siteBase != "" {
		base = siteBase
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch {
	case u.Path == newestPath,
		host == "conorcoxxx.com" && (u.Path == "/conor-coxxx-clips.html" || u.Path == "/horny-household-clips.html"):
		return base, u.Path
	case host == "conorcoxxx.com":
		return base, "/conor-coxxx-clips.html"
	}
	return base, newestPath
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base, path := listingFor(studioURL)
	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?page=%d", base, path, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body, base)
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
	// The CMS wraps attributes across lines, so pairs tolerate any whitespace.
	cardRe      = regexp.MustCompile(`<div class="grid-item"\s+id="ascene_(\d+)"`)
	linkRe      = regexp.MustCompile(`<a class="scene-img"\s+href="(/[^"]+)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<a class="scene-title".*?<h6>\s*(.*?)\s*</h6>`)
	cardPerfRe  = regexp.MustCompile(`(?s)<p class="scene-performer-names">\s*(.*?)\s*</p>`)
	cardLenRe   = regexp.MustCompile(`(?s)<p class="scene-length">\s*(\d+)\s*min`)
	thumbRe     = regexp.MustCompile(`data-srcset="(https://[^"\s]+)\s+700w`)
)

type listItem struct {
	id, url, title, thumb string
	performers            []string
	minutes               int
}

func parseListing(body []byte, base string) []listItem {
	page := string(body)
	locs := cardRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]listItem, 0, len(locs))

	for i, loc := range locs {
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		card := page[loc[0]:end]

		m := linkRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{id: page[loc[2]:loc[3]], url: base + m[1]}

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
		if l := cardLenRe.FindStringSubmatch(card); l != nil {
			it.minutes = atoi(l[1])
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = html.UnescapeString(th[1])
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
	titleRe    = regexp.MustCompile(`(?s)<h1 class="description">\s*(.*?)\s*</h1>`)
	synopsisRe = regexp.MustCompile(`(?s)<div class="synopsis">\s*(.*?)\s*</div>`)
	releasedRe = regexp.MustCompile(`(?s)Released:</span>\s*([A-Z][a-z]{2} \d{1,2}, \d{4})`)
	lengthRe   = regexp.MustCompile(`(?s)Length:</span>\s*(\d+)\s*min`)
	directorRe = regexp.MustCompile(`(?s)Director:</span>\s*([^<]+?)\s*<`)
	studioRe   = regexp.MustCompile(`(?s)Studio:</span>\s*<span>\s*([^<]+?)\s*</span>`)
	perfRe     = regexp.MustCompile(`(?s)<div class="performer-name">\s*(.*?)\s*</div>`)
	tagRe      = regexp.MustCompile(`data-Label="Tag"[^>]*>\s*([^<]+?)\s*</a>`)
	markupRe   = regexp.MustCompile(`<[^>]+>`)
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
		Duration:   it.minutes * 60,
		Studio:     defaultStudio,
		ScrapedAt:  now,
	}

	body, err := s.fetchPage(ctx, it.url)
	if err != nil {
		scraper.Debugf(1, "%s: detail %s: %v", siteID, it.url, err)
		return scene
	}
	applyDetail(&scene, string(body))
	return scene
}

func applyDetail(scene *models.Scene, detail string) {
	if m := titleRe.FindStringSubmatch(detail); m != nil {
		if t := cleanText(m[1]); t != "" {
			scene.Title = t
		}
	}
	if m := synopsisRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(markupRe.ReplaceAllString(m[1], " "))
	}
	if m := releasedRe.FindStringSubmatch(detail); m != nil {
		if ts, err := time.Parse(dateLayout, m[1]); err == nil {
			scene.Date = ts.UTC()
		}
	}
	if m := lengthRe.FindStringSubmatch(detail); m != nil {
		scene.Duration = atoi(m[1]) * 60
	}
	if m := directorRe.FindStringSubmatch(detail); m != nil {
		scene.Director = cleanText(m[1])
	}
	if m := studioRe.FindStringSubmatch(detail); m != nil {
		if n := strings.TrimSpace(strings.TrimSuffix(cleanText(m[1]), " Clips")); n != "" {
			scene.Studio = n
		}
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
