// Package raunchybastards scrapes raunchybastards.com. See docs/scrapers.md.
package raunchybastards

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "raunchybastards"
	studioName = "Raunchy Bastards"
	// detailWorkers caps the detail pool; the catalogue is ~456 scenes.
	detailWorkers = 4
	// sortNewest is passed on every mode: the default order is the editor's
	// pick, under which the KnownIDs early-stop would truncate arbitrarily.
	sortNewest = "published-newer"
)

var (
	matchRe     = regexp.MustCompile(`^https?://(?:www\.)?raunchybastards\.com(?:/|$)`)
	categoryRe  = regexp.MustCompile(`^/scenes/category/[^/?#]+$`)
	profileRe   = regexp.MustCompile(`^/profile/[^/?#]+$`)
	cardSplitRe = regexp.MustCompile(`<div class="scene_container`)
	cardLinkRe  = regexp.MustCompile(`href="/scene/(\d+)-([^"?#]+)"`)
	cardThumbRe = regexp.MustCompile(`<img src="([^"]+)"[^>]*class="img-responsive"`)
	cardTitleRe = regexp.MustCompile(`(?s)<div class="wrapperSceneTitle">\s*<a[^>]*>(.*?)</a>`)
	cardModelRe = regexp.MustCompile(`(?s)<h4>(.*?)</h4>`)
	cardMinsRe  = regexp.MustCompile(`(\d+)\s*min`)
	cardLikesRe = regexp.MustCompile(`class="[^"]*likesLbl[^"]*"[^>]*>(\d+)</span>`)

	detailDescRe = regexp.MustCompile(`(?s)<div class="p-5">\s*<p>(.*?)</p>`)
	detailCatsRe = regexp.MustCompile(`(?s)<h5 class="strong">Categories:(.*?)</h5>`)
	anchorTextRe = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
)

// Scraper implements scraper.StudioScraper for raunchybastards.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Raunchy Bastards scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://www.raunchybastards.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"raunchybastards.com",
		"raunchybastards.com/scenes/category/{id}-{slug}",
		"raunchybastards.com/profile/{id}-{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one listing entry, before the detail page is fetched.
type card struct {
	id         string
	slug       string
	title      string
	thumb      string
	performers []string
	duration   int
	likes      int
}

// listingPath maps an operator URL onto the listing it selects. A category or
// profile page is the same grid filtered; anything else is the catalogue.
func listingPath(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "/scenes"
	}
	p := strings.TrimSuffix(u.Path, "/")
	if categoryRe.MatchString(p) || profileRe.MatchString(p) {
		return p
	}
	return "/scenes"
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	path := listingPath(studioURL)
	scraper.Debugf(1, "%s: listing %s", siteID, path)

	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?sort=%s&page=%d", s.base, path, sortNewest, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}

		// Past the last page the CMS clamps back to page 1 rather than serving
		// an empty grid, so a page that adds nothing new is the end.
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
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// parseListing reads the scene cards. The cards are sliced between their
// opening markers: the grid nests divs and shares the closing tags.
func parseListing(body string) []card {
	marks := cardSplitRe.FindAllStringIndex(body, -1)
	cards := make([]card, 0, len(marks))
	for i, m := range marks {
		end := len(body)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		block := body[m[1]:end]

		link := cardLinkRe.FindStringSubmatch(block)
		if link == nil {
			continue
		}
		c := card{id: link[1], slug: link[2]}
		if t := cardTitleRe.FindStringSubmatch(block); t != nil {
			c.title = cleanText(t[1])
		}
		if th := cardThumbRe.FindStringSubmatch(block); th != nil {
			c.thumb = html.UnescapeString(th[1])
		}
		if h := cardModelRe.FindStringSubmatch(block); h != nil {
			for _, a := range anchorTextRe.FindAllStringSubmatch(h[1], -1) {
				if name := cleanText(a[1]); name != "" {
					c.performers = append(c.performers, name)
				}
			}
		}
		if d := cardMinsRe.FindStringSubmatch(block); d != nil {
			if n, err := strconv.Atoi(d[1]); err == nil {
				c.duration = n * 60
			}
		}
		if l := cardLikesRe.FindStringSubmatch(block); l != nil {
			c.likes, _ = strconv.Atoi(l[1])
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:         c.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     studioName,
		Title:      c.title,
		URL:        fmt.Sprintf("%s/scene/%s-%s", s.base, c.id, c.slug),
		Thumbnail:  c.thumb,
		Performers: c.performers,
		Duration:   c.duration,
		Likes:      c.likes,
		ScrapedAt:  now,
	}
}

// enrich fetches each scene's detail page for the description and categories.
// A failure keeps the listing-only record.
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
			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
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
// The site publishes no release date anywhere, so scenes are stored undated.
func applyDetail(sc *models.Scene, body string) {
	if d := detailDescRe.FindStringSubmatch(body); d != nil {
		sc.Description = cleanText(d[1])
	}
	if c := detailCatsRe.FindStringSubmatch(body); c != nil {
		for _, a := range anchorTextRe.FindAllStringSubmatch(c[1], -1) {
			if name := cleanText(a[1]); name != "" {
				sc.Categories = append(sc.Categories, name)
			}
		}
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
	s = strings.ReplaceAll(s, "<br />", " ")
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}
