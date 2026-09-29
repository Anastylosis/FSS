// Package brickyates scrapes brickyates.com, an Elevated X tour on a custom
// skin. See docs/scrapers.md.
package brickyates

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
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

const (
	siteID     = "brickyates"
	studioName = "Brick Yates"
	// defaultCategory is the whole video catalogue; the tour's other half,
	// "photos", is not scenes.
	defaultCategory = "Movies"
	// detailWorkers caps the per-page detail pool; the listing serves 12 cards
	// a page over ~26 pages and every field but the title is on the scene's
	// own page.
	detailWorkers = 4
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?brickyates\.com(?:/|$)`)
	categoryRe = regexp.MustCompile(`/tour/categories/([^/]+)/`)
	modelRe    = regexp.MustCompile(`/tour/models/([^/?#]+)\.html`)

	// Cards are sliced between their opening markers; the grid shares its
	// closing tags.
	cardSplitRe = regexp.MustCompile(`id="set-target-(\d+)"`)
	cardLinkRe  = regexp.MustCompile(`href="[^"]*(/tour/trailers/[^"?#]+\.html)" title="([^"]*)"`)
	cardThumbRe = regexp.MustCompile(`src0_1x="([^"]+)"`)

	detailTitleRe = regexp.MustCompile(`(?s)<div class="videoDetails[^"]*">\s*<h1>(.*?)</h1>\s*(?:<p>(.*?)</p>)?`)
	detailInfoRe  = regexp.MustCompile(`(?s)<div class="videoInfo[^"]*">(.*?)</div>`)
	dateRe        = regexp.MustCompile(`Date Added:</span>\s*([A-Za-z]+ \d{1,2}, \d{4})`)
	minutesRe     = regexp.MustCompile(`(\d+)(?:&nbsp;|\s)*minute`)
	featuringRe   = regexp.MustCompile(`(?s)<div class="featuring[^"]*">(.*?)</div>`)
	modelItemRe   = regexp.MustCompile(`(?s)<li class="update_models">\s*<a[^>]*>(.*?)</a>`)
	tagItemRe     = regexp.MustCompile(`(?s)<a[^>]*/tour/categories/[^>]*>(.*?)</a>`)
	posterRe      = regexp.MustCompile(`poster="([^"]+)"`)

	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

// dateLayouts cover the detail page's "January 23, 2024".
var dateLayouts = []string{"January 2, 2006", "January 02, 2006"}

// Scraper implements scraper.StudioScraper for brickyates.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Brick Yates scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://www.brickyates.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"brickyates.com",
		"brickyates.com/tour/categories/{category}/{page}/latest/",
		"brickyates.com/tour/models/{Name}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one listing entry. It carries only the title, so every scene needs
// its detail page.
type card struct {
	id    string
	path  string
	title string
	thumb string
}

// listing describes what an operator URL selects: a paginated category, or a
// model's own page, which is a single page.
type listing struct {
	category string
	model    string
}

// resolveListing maps an operator URL onto its listing. Anything that is not a
// category or model page walks the whole Movies category.
func resolveListing(studioURL string) listing {
	u, err := url.Parse(studioURL)
	if err != nil {
		return listing{category: defaultCategory}
	}
	if m := modelRe.FindStringSubmatch(u.Path); m != nil && m[1] != "" {
		return listing{model: m[1]}
	}
	if m := categoryRe.FindStringSubmatch(u.Path); m != nil && m[1] != "" {
		return listing{category: m[1]}
	}
	return listing{category: defaultCategory}
}

func (s *Scraper) listingURL(l listing, page int) string {
	if l.model != "" {
		return fmt.Sprintf("%s/tour/models/%s.html", s.base, l.model)
	}
	return fmt.Sprintf("%s/tour/categories/%s/%d/latest/", s.base, l.category, page)
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

		fresh := cards[:0]
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			fresh = append(fresh, c)
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
			scenes[i] = s.baseScene(c, studioURL, now)
		}
		s.enrich(ctx, scenes[:enrichTo], opts, out)
		return scraper.PageResult{Scenes: scenes, Done: l.model != ""}, nil
	})
}

// parseListing reads the listing's scene cards.
func parseListing(body string) []card {
	marks := cardSplitRe.FindAllStringSubmatchIndex(body, -1)
	cards := make([]card, 0, len(marks))
	for i, m := range marks {
		start := m[0]
		end := len(body)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		// The card's link precedes its image, so the block starts at the
		// previous card's end rather than at the id itself.
		blockStart := 0
		if i > 0 {
			blockStart = marks[i-1][1]
		}
		block := body[blockStart:end]

		link := cardLinkRe.FindStringSubmatch(block)
		if link == nil {
			continue
		}
		c := card{id: body[m[2]:m[3]], path: link[1], title: cleanText(link[2])}
		if th := cardThumbRe.FindStringSubmatch(body[start:end]); th != nil {
			c.thumb = html.UnescapeString(th[1])
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:        c.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     c.title,
		URL:       s.base + c.path,
		Thumbnail: resolveURL(s.base, c.thumb),
		ScrapedAt: now,
	}
}

// enrich fetches each scene's detail page, which carries the synopsis, date,
// runtime, cast and tags.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	if len(scenes) == 0 {
		return
	}
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
			applyDetail(&scenes[i], body, s.base)
		}(i)
	}
	wg.Wait()
}

// applyDetail folds the detail page into a scene built from its listing card.
func applyDetail(sc *models.Scene, body, base string) {
	if t := detailTitleRe.FindStringSubmatch(body); t != nil {
		if title := cleanText(t[1]); title != "" {
			sc.Title = title
		}
		sc.Description = cleanText(t[2])
	}
	if info := detailInfoRe.FindStringSubmatch(body); info != nil {
		if d := dateRe.FindStringSubmatch(info[1]); d != nil {
			if parsed, err := parseutil.TryParseDate(d[1], dateLayouts...); err == nil {
				sc.Date = parsed
			}
		}
		if m := minutesRe.FindStringSubmatch(info[1]); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				sc.Duration = n * 60
			}
		}
	}
	// The cast and the tags share the "featuring" block shape; the cast is the
	// one whose items carry the update_models class.
	for _, blk := range featuringRe.FindAllStringSubmatch(body, -1) {
		if ms := modelItemRe.FindAllStringSubmatch(blk[1], -1); len(ms) > 0 {
			for _, m := range ms {
				if name := cleanText(m[1]); name != "" {
					sc.Performers = append(sc.Performers, name)
				}
			}
			continue
		}
		for _, m := range tagItemRe.FindAllStringSubmatch(blk[1], -1) {
			if tag := cleanText(m[1]); tag != "" {
				sc.Tags = append(sc.Tags, tag)
			}
		}
	}
	if sc.Thumbnail == "" {
		if p := posterRe.FindStringSubmatch(body); p != nil {
			sc.Thumbnail = resolveURL(base, html.UnescapeString(p[1]))
		}
	}
}

// resolveURL turns the tour's relative and protocol-relative paths into
// absolute ones.
func resolveURL(base, ref string) string {
	switch {
	case ref == "":
		return ""
	case strings.HasPrefix(ref, "//"):
		return "https:" + ref
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return ref
	case strings.HasPrefix(ref, "/"):
		return base + ref
	default:
		return base + "/" + ref
	}
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
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}
