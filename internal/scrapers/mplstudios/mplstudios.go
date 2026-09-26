// Package mplstudios scrapes MPL Studios (mplstudios.com), an artistic-nude
// photo and video site running a bespoke PHP CMS.
//
// Two URL modes are supported:
//
//   - the video listing at /videos/ (paginated as /videos/{N}/), which is
//     sorted newest-first so KnownIDs early-stop applies; and
//   - a model portfolio at /portfolio/{id}-{Name}/, which renders every update
//     for that model on a single page — photo sets carry the "updatePhoto"
//     class and videos "updateVideo", so only the latter are returned.
//
// Both modes share the same grid markup: a "box1" card holding the /update/
// link, cover image, release date, model, title and photographer. Duration
// lives only on the detail page ("Movie Length: MM:SS"), so a small worker
// pool enriches each card.
package mplstudios

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "mplstudios"
	studioName    = "MPL Studios"
	detailWorkers = 4
	dateLayout    = "Jan 2, 2006"
)

var siteBase = "https://www.mplstudios.com"

// Scraper implements scraper.StudioScraper for MPL Studios.
type Scraper struct {
	Client *http.Client
}

// New constructs an MPL Studios scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"mplstudios.com/videos/",
		"mplstudios.com/portfolio/{id}-{Model_Name}/",
	}
}

var (
	matchRe     = regexp.MustCompile(`^https?://(?:www\.)?mplstudios\.com(?:/|$)`)
	portfolioRe = regexp.MustCompile(`/portfolio/(\d+)-([^/]+)`)
)

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
	if m := portfolioRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping model portfolio %s", siteID, m[2])
		s.runPortfolio(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, out, now, opts.Delay)
		return
	}
	scraper.Debugf(1, "%s: scraping video listing", siteID)
	s.runListing(ctx, studioURL, opts, out, now)
}

// runListing walks /videos/{N}/, which is ordered newest-first.
func (s *Scraper) runListing(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.get(ctx, fmt.Sprintf("%s/videos/%d/", siteBase, page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseCards(body, false)
		if len(cards) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, cards, now, opts.Delay)}, nil
	})
}

// runPortfolio scrapes a single model page, which lists every update at once.
func (s *Scraper) runPortfolio(ctx context.Context, workers int, studioURL string, out chan<- scraper.SceneResult, now time.Time, delay time.Duration) {
	body, err := s.get(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	cards := parseCards(body, true)
	scraper.Debugf(1, "%s: portfolio has %d videos", siteID, len(cards))

	select {
	case out <- scraper.Progress(len(cards)):
	case <-ctx.Done():
		return
	}

	for _, scene := range s.enrich(ctx, workers, studioURL, cards, now, delay) {
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}
}

// ---- listing parsing ----

var (
	// scriptRe strips <script> blocks before parsing. The page embeds a JS
	// template that reproduces the card markup verbatim for infinite scroll;
	// left in place it would parse as a bogus card.
	scriptRe = regexp.MustCompile(`(?s)<script\b.*?</script>`)
	// gridRe locates the results container — "updateGrid" on listing pages,
	// "modelGrid" on portfolios — so navigation and footer markup is excluded.
	gridRe = regexp.MustCompile(`id="(?:updateGrid|modelGrid)"`)
	// cardStartRe matches one grid card's opening tag. Listing pages use
	// `class="text-center mb-3 box1"`; portfolio pages append the
	// `updatePhoto`/`updateVideo` filter class, captured in group 1.
	cardStartRe = regexp.MustCompile(`<div class="text-center mb-3 box1([^"]*)"`)
	updateRe    = regexp.MustCompile(`href="/update/([^/"]+)/"`)
	coverRe     = regexp.MustCompile(`class="stdCover[^"]*"\s+src="([^"]+)"`)
	dateRe      = regexp.MustCompile(`<span class="ellipsis">([A-Z][a-z]{2} \d{1,2}, \d{4})</span>`)
	modelRe     = regexp.MustCompile(`href="/portfolio/\d+-[^"]*"[^>]*>([^<]+)</a>`)
	titleRe     = regexp.MustCompile(`<span class="ellipsis" title="([^"]*)"`)
	photogRe    = regexp.MustCompile(`href="/photographers/\d+-[^"]*"[^>]*>([^<]+)</a>`)
	lengthRe    = regexp.MustCompile(`Movie Length:\s*(\d{1,2}:\d{2}(?::\d{2})?)`)
	slugTrail   = regexp.MustCompile(`^([0-9]+[a-z]*[0-9]*)-(.*)$`)
)

type card struct {
	id           string
	url          string
	title        string
	cover        string
	date         time.Time
	model        string
	photographer string
}

// parseCards extracts grid cards from a listing or portfolio page. When
// videosOnly is set (portfolio pages), cards not carrying the "updateVideo"
// class are dropped — those are photo sets.
//
// Cards are delimited by index rather than by a closing tag: the CMS nests
// <div>s inside a card, so a lazy match to </div> would truncate. Each card's
// body runs from its opening tag to the next card's, and every field regex
// takes its first match, which always lies inside the card proper.
func parseCards(body []byte, videosOnly bool) []card {
	region := scriptRe.ReplaceAll(body, nil)
	if loc := gridRe.FindIndex(region); loc != nil {
		region = region[loc[1]:]
	}

	starts := cardStartRe.FindAllSubmatchIndex(region, -1)
	var cards []card
	seen := make(map[string]bool)
	for i, loc := range starts {
		end := len(region)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		classes := string(region[loc[2]:loc[3]])
		if videosOnly && !strings.Contains(classes, "updateVideo") {
			continue
		}
		c, ok := parseCard(string(region[loc[1]:end]))
		if !ok || seen[c.id] {
			continue
		}
		seen[c.id] = true
		cards = append(cards, c)
	}
	return cards
}

func parseCard(inner string) (card, bool) {
	m := updateRe.FindStringSubmatch(inner)
	if m == nil {
		return card{}, false
	}
	slug := m[1]
	c := card{url: siteBase + "/update/" + slug + "/"}

	// "/update/6928v-Unplugged/" -> id "6928v", fallback title "Unplugged".
	if sm := slugTrail.FindStringSubmatch(slug); sm != nil {
		c.id = sm[1]
		c.title = strings.ReplaceAll(sm[2], "_", " ")
	} else {
		c.id = slug
	}

	if t := titleRe.FindStringSubmatch(inner); t != nil {
		c.title = html.UnescapeString(t[1])
	}
	if cv := coverRe.FindStringSubmatch(inner); cv != nil {
		c.cover = cv[1]
	}
	if d := dateRe.FindStringSubmatch(inner); d != nil {
		if parsed, err := time.Parse(dateLayout, d[1]); err == nil {
			c.date = parsed.UTC()
		}
	}
	if md := modelRe.FindStringSubmatch(inner); md != nil {
		c.model = html.UnescapeString(strings.TrimSpace(md[1]))
	}
	if p := photogRe.FindStringSubmatch(inner); p != nil {
		c.photographer = html.UnescapeString(strings.TrimSpace(p[1]))
	}
	return c, true
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, cards []card, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(cards))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(cards), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, c := range cards {
		wg.Add(1)
		go func(i int, c card) {
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
			scenes[i] = s.toScene(ctx, studioURL, c, now)
		}(i, c)
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

func (s *Scraper) toScene(ctx context.Context, studioURL string, c card, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        c.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     c.title,
		URL:       c.url,
		Thumbnail: c.cover,
		Date:      c.date,
		Studio:    studioName,
		Director:  c.photographer,
		ScrapedAt: now,
	}
	if c.model != "" {
		scene.Performers = []string{c.model}
	}

	// The listing carries everything except duration; the detail page is
	// fetched purely for "Movie Length". A failure here is not fatal.
	if body, err := s.get(ctx, c.url); err == nil {
		if m := lengthRe.FindSubmatch(body); m != nil {
			scene.Duration = parseutil.ParseDurationColon(string(m[1]))
		}
	}
	return scene
}

// ---- HTTP ----

func (s *Scraper) get(ctx context.Context, rawURL string) ([]byte, error) {
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
