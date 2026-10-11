// Package oldnanny scrapes the OldNanny network tour (oldnanny.com), which
// hosts Old Nanny, Aged Love and five sibling sites as `site` filters of one
// listing. See docs/scrapers.md.
package oldnanny

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID      = "oldnanny"
	networkName = "Old Nanny"
	// Every scene needs its detail page for duration, tags and the site name.
	detailWorkers = 4
)

// siteNames maps the tour's `site` slugs to their display names; an unknown
// slug falls back to the detail page's own label.
var siteNames = map[string]string{
	"OldNanny":     "Old Nanny",
	"AgedLove":     "Aged Love",
	"OmaHotel":     "Oma Hotel",
	"OmaHunter":    "Oma Hunter",
	"UsaWives":     "USA Wives",
	"EuropeMature": "Europe Mature",
	"FrenzyBDSM":   "Frenzy BDSM",
	"LatinChili":   "Latin Chili",
}

var (
	oldNannyRe = regexp.MustCompile(`^https?://(?:www\.)?oldnanny\.com(?:/|$|\?)`)
	agedLoveRe = regexp.MustCompile(`^https?://(?:www\.)?agedlove\.com(?:/|$|\?)`)
	filterRe   = regexp.MustCompile(`/tour2/scenes/(site|model|tag)/([^?#]+)`)

	cardSplitRe = regexp.MustCompile(`<article class="public-tour-card">`)
	cardLinkRe  = regexp.MustCompile(`href="(/[a-z]{2}/tour2/video/([^/"]+)/[^"]+)"`)
	cardThumbRe = regexp.MustCompile(`(?s)public-tour-card__media".*?<img src="([^"]+)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<h3><a[^>]*>(.*?)</a></h3>`)
	cardModelRe = regexp.MustCompile(`href="/[a-z]{2}/tour2/scenes/model/[^"]+">([^<]+)</a>`)
	cardDateRe  = regexp.MustCompile(`public-tour-card__date">([^<]+)<`)

	ldRe       = regexp.MustCompile(`(?s)<script type="application/ld\+json">(\{[^<]*"@type":"VideoObject"[^<]*\})</script>`)
	durRe      = regexp.MustCompile(`"duration":"PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?"`)
	uploadRe   = regexp.MustCompile(`"uploadDate":"(\d{4}-\d{2}-\d{2})`)
	detSiteRe  = regexp.MustCompile(`(?s)<dt>Site</dt>.*?scenes/site/([A-Za-z0-9_-]+)">([^<]*)</a>`)
	detTagRe   = regexp.MustCompile(`public-media-detail__tag"[^>]*>([^<]+)</a>`)
	detModelRe = regexp.MustCompile(`public-media-detail__model[^"]*"[^>]*>([^<]+)</a>`)
)

var cardDateLayouts = []string{"Jan 2, 2006", "Jan 02, 2006"}

// Scraper implements scraper.StudioScraper for oldnanny.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Old Nanny network scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://oldnanny.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"oldnanny.com",
		"oldnanny.com/en/tour2/scenes/all",
		"oldnanny.com/en/tour2/scenes/site/{Site}",
		"oldnanny.com/en/tour2/scenes/model/{slug}",
		"oldnanny.com/en/tour2/scenes/tag/{id}/{slug}",
		"agedlove.com",
	}
}

func (s *Scraper) MatchesURL(u string) bool {
	return oldNannyRe.MatchString(u) || agedLoveRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// listing is what an operator URL selects, as the path below /en/tour2/.
type listing struct {
	path string
	// site is the `site` slug when the listing is one site's filter.
	site string
}

func resolveListing(studioURL string) listing {
	if agedLoveRe.MatchString(studioURL) {
		return listing{path: "scenes/site/AgedLove", site: "AgedLove"}
	}
	if u, err := url.Parse(studioURL); err == nil {
		if m := filterRe.FindStringSubmatch(u.Path); m != nil {
			p := strings.TrimSuffix(m[2], "/")
			l := listing{path: "scenes/" + m[1] + "/" + p}
			if m[1] == "site" {
				l.site = p
			}
			return l
		}
	}
	return listing{path: "scenes/all"}
}

func (s *Scraper) listingURL(l listing, page int) string {
	return fmt.Sprintf("%s/en/tour2/%s?page=%d", s.base, l.path, page)
}

type card struct {
	id        string
	path      string
	title     string
	thumb     string
	performer []string
	date      time.Time
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	l := resolveListing(studioURL)
	scraper.Debugf(1, "%s: listing %+v", siteID, l)

	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(l, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}

		// Pages past the end repeat the last page rather than 404ing.
		fresh := make([]card, 0, len(cards))
		for _, c := range cards {
			if !seen[c.id] {
				seen[c.id] = true
				fresh = append(fresh, c)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		enrichTo := len(fresh)
		for i, c := range fresh {
			if opts.KnownIDs[c.id] {
				enrichTo = i
				break
			}
		}
		scenes := make([]models.Scene, len(fresh))
		for i, c := range fresh {
			scenes[i] = s.baseScene(c, l, studioURL, now)
		}
		s.enrich(ctx, scenes[:enrichTo], opts, out)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func parseListing(body string) []card {
	idx := cardSplitRe.FindAllStringIndex(body, -1)
	cards := make([]card, 0, len(idx))
	for i, m := range idx {
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		block := body[m[0]:end]
		link := cardLinkRe.FindStringSubmatch(block)
		if link == nil {
			continue
		}
		c := card{id: link[2], path: link[1]}
		if t := cardTitleRe.FindStringSubmatch(block); t != nil {
			c.title = cleanText(t[1])
		}
		if th := cardThumbRe.FindStringSubmatch(block); th != nil {
			c.thumb = html.UnescapeString(th[1])
		}
		for _, p := range cardModelRe.FindAllStringSubmatch(block, -1) {
			c.performer = append(c.performer, cleanText(p[1]))
		}
		if d := cardDateRe.FindStringSubmatch(block); d != nil {
			if t, err := parseutil.TryParseDate(strings.TrimSpace(d[1]), cardDateLayouts...); err == nil {
				c.date = t
			}
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) baseScene(c card, l listing, studioURL string, now time.Time) models.Scene {
	studio := networkName
	if l.site != "" {
		studio = displayName(l.site, "")
	}
	return models.Scene{
		ID:         c.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     studio,
		Title:      c.title,
		URL:        s.base + c.path,
		Thumbnail:  c.thumb,
		Performers: c.performer,
		Date:       c.date,
		ScrapedAt:  now,
	}
}

func displayName(slug, label string) string {
	if n, ok := siteNames[slug]; ok {
		return n
	}
	if label = strings.TrimSpace(label); label != "" {
		return label
	}
	return slug
}

func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(scenes) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range scenes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(&scenes[i], body)
		}(i)
	}
	wg.Wait()
}

// applyDetail folds the detail page into a scene built from its listing card.
func applyDetail(sc *models.Scene, body string) {
	if ld := ldRe.FindStringSubmatch(body); ld != nil {
		if m := durRe.FindStringSubmatch(ld[1]); m != nil {
			sc.Duration = atoi(m[1])*3600 + atoi(m[2])*60 + atoi(m[3])
		}
		if m := uploadRe.FindStringSubmatch(ld[1]); m != nil {
			if t, err := parseutil.TryParseDate(m[1], "2006-01-02"); err == nil {
				sc.Date = t
			}
		}
	}
	if m := detSiteRe.FindStringSubmatch(body); m != nil {
		sc.Studio = displayName(m[1], m[2])
	}
	for _, m := range detTagRe.FindAllStringSubmatch(body, -1) {
		if tag := cleanText(m[1]); tag != "" {
			sc.Tags = append(sc.Tags, tag)
		}
	}
	if len(sc.Performers) == 0 {
		for _, m := range detModelRe.FindAllStringSubmatch(body, -1) {
			if name := cleanText(m[1]); name != "" {
				sc.Performers = append(sc.Performers, name)
			}
		}
	}
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}
