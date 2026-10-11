package seemomsuck

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const defaultSiteBase = "https://www.seemomsuck.com"

type Scraper struct {
	client   *http.Client
	siteBase string
}

func New() *Scraper {
	return &Scraper{
		client:   httpx.NewClient(30 * time.Second),
		siteBase: defaultSiteBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "seemomsuck" }

func (s *Scraper) Patterns() []string {
	return []string{
		"seemomsuck.com",
		"seemomsuck.com/models/{name}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?seemomsuck\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	if isModelURL(studioURL) {
		scraper.Debugf(1, "seemomsuck: scraping model page")
		s.scrapeModelPage(ctx, studioURL, opts, out, now)
	} else {
		s.scrapeListingPages(ctx, studioURL, opts, out, now)
	}
}

// isModelURL matches both the current URL scheme (/models/{slug}) and the
// site's old pre-migration form (/models/{slug}.html); the site 301-redirects
// the latter to the former, but a user's saved URL may still use it.
func isModelURL(u string) bool {
	return strings.Contains(u, "/models/")
}

// The site is now a Laravel/Tailwind rebuild (formerly a static-HTML CMS).
// Listings live at /videos (sort=date preserves reverse-chronological order,
// matching the plain /videos "New" tab default) with page-numbered
// pagination via ?page=N.
func (s *Scraper) scrapeListingPages(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	scraper.Paginate(ctx, opts, "seemomsuck", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.siteBase + "/videos?sort=date"
		if page > 1 {
			pageURL = fmt.Sprintf("%s/videos?sort=date&page=%d", s.siteBase, page)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListingCards(body)
		scenes := make([]models.Scene, len(items))
		for i, ps := range items {
			scenes[i] = ps.toScene(s.siteBase, studioURL, now)
		}

		maxPage := extractMaxPage(body)
		var total int
		if page == 1 && len(items) > 0 {
			total = len(items) * maxPage
		}

		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   page >= maxPage || len(items) == 0,
		}, nil
	})
}

// Model pages moved from /models/{slug}.html to /models/{slug} (no
// extension). In current practice each model's catalog fits on a single
// page, but the same ?page=N pagination nav used by /videos can appear
// there too, so we handle it the same way.
func (s *Scraper) scrapeModelPage(ctx context.Context, modelURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	baseURL := cleanModelURL(modelURL)
	var modelName string

	scraper.Paginate(ctx, opts, "seemomsuck", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := baseURL
		if page > 1 {
			sep := "?"
			if strings.Contains(baseURL, "?") {
				sep = "&"
			}
			pageURL = fmt.Sprintf("%s%spage=%d", baseURL, sep, page)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		if page == 1 {
			modelName = extractModelName(body)
		}

		items := parseModelCards(body)
		scenes := make([]models.Scene, len(items))
		for i, ps := range items {
			if len(ps.performers) == 0 && modelName != "" {
				ps.performers = []string{modelName}
			}
			scenes[i] = ps.toScene(s.siteBase, modelURL, now)
		}

		maxPage := extractMaxPage(body)
		var total int
		if page == 1 && len(items) > 0 {
			total = len(items) * maxPage
		}

		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   page >= maxPage || len(items) == 0,
		}, nil
	})
}

// parsedScene holds the fields common to both the /videos listing cards and
// the /models/{slug} cards; the model page cards don't carry a description,
// published date, or view count, so those fields are left zero there.
type parsedScene struct {
	id          string
	title       string
	performers  []string
	description string
	thumb       string
	published   time.Time
	views       int
}

func (ps parsedScene) toScene(siteBase, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          ps.id,
		SiteID:      "seemomsuck",
		StudioURL:   studioURL,
		Title:       ps.title,
		URL:         siteBase + "/video/" + ps.id,
		Thumbnail:   ps.thumb,
		Description: ps.description,
		Performers:  ps.performers,
		Studio:      "See Mom Suck",
		Views:       ps.views,
		ScrapedAt:   now,
	}
	if !ps.published.IsZero() {
		sc.Date = ps.published
	}
	return sc
}

var (
	// /videos listing cards.
	listingCardStartRe = regexp.MustCompile(`<div class="flex flex-col md:flex-row md:items-start gap-6 lg:gap-10 mb-10 md:mb-12 py-2 md:py-4 lg:py-6">`)
	listingTitleRe     = regexp.MustCompile(`(?s)<h2[^>]*>\s*<a[^>]*>([^<]*)</a>`)
	publishedViewsRe   = regexp.MustCompile(`Published\s+([A-Za-z]+ \d{1,2},\s*\d{4})\s*\S\s*([\d,]+)\s*views`)
	descriptionRe      = regexp.MustCompile(`(?s)class="mt-4 mb-4 md:mb-6 leading-relaxed[^"]*">\s*(.*?)\s*</p>`)

	// /models/{slug} cards.
	modelCardStartRe = regexp.MustCompile(`<div class="group cursor-pointer hover-video"`)
	modelTitleRe     = regexp.MustCompile(`(?s)<h3[^>]*>\s*([^<]+?)\s*</h3>`)
	modelNameRe      = regexp.MustCompile(`(?s)<h1\s+class="text-3xl lg:text-4xl font-bold text-white mb-8 tracking-wide uppercase"\s*>\s*([^<]+?)\s*</h1>`)

	// Shared across both card types.
	videoLinkRe     = regexp.MustCompile(`href="[^"]*/video/(\d+)"`)
	performerLinkRe = regexp.MustCompile(`href="[^"]*/models/[^"]+"[^>]*>\s*([^<]+?)\s*</a>`)
	thumbRe         = regexp.MustCompile(`<img\s+src="([^"]+)"`)
	pageNumRe       = regexp.MustCompile(`page=(\d+)`)
)

func parseListingCards(body []byte) []parsedScene {
	page := string(body)
	locs := listingCardStartRe.FindAllStringIndex(page, -1)
	scenes := make([]parsedScene, 0, len(locs))

	for i, loc := range locs {
		start := loc[0]
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[start:end]

		linkM := videoLinkRe.FindStringSubmatch(block)
		if linkM == nil {
			continue
		}
		ps := parsedScene{id: linkM[1]}

		if m := listingTitleRe.FindStringSubmatch(block); m != nil {
			ps.title = strings.TrimSpace(html.UnescapeString(m[1]))
		}

		if m := publishedViewsRe.FindStringSubmatch(block); m != nil {
			if t, err := parseutil.TryParseDate(strings.TrimSpace(m[1]), "Jan 2, 2006"); err == nil {
				ps.published = t
			}
			if v, err := strconv.Atoi(strings.ReplaceAll(m[2], ",", "")); err == nil {
				ps.views = v
			}
		}

		for _, pm := range performerLinkRe.FindAllStringSubmatch(block, -1) {
			name := strings.TrimSpace(html.UnescapeString(pm[1]))
			if name != "" {
				ps.performers = append(ps.performers, name)
			}
		}

		if m := descriptionRe.FindStringSubmatch(block); m != nil {
			ps.description = cleanDescription(m[1])
		}

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			ps.thumb = m[1]
		}

		scenes = append(scenes, ps)
	}
	return scenes
}

// parseModelCards parses a /models/{slug} page. The site renders a "More
// Scenes With {Model}" cross-promo section using the exact same card markup
// but linking to a signup page on a different network site instead of a
// /video/{id} URL — those blocks are skipped because videoLinkRe won't match.
func parseModelCards(body []byte) []parsedScene {
	page := string(body)
	locs := modelCardStartRe.FindAllStringIndex(page, -1)
	scenes := make([]parsedScene, 0, len(locs))

	for i, loc := range locs {
		start := loc[0]
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[start:end]

		linkM := videoLinkRe.FindStringSubmatch(block)
		if linkM == nil {
			continue
		}
		ps := parsedScene{id: linkM[1]}

		if m := modelTitleRe.FindStringSubmatch(block); m != nil {
			ps.title = strings.TrimSpace(html.UnescapeString(m[1]))
		}

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			ps.thumb = m[1]
		}

		scenes = append(scenes, ps)
	}
	return scenes
}

func cleanDescription(s string) string {
	s = strings.TrimSpace(s)
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	return s
}

func extractModelName(body []byte) string {
	if m := modelNameRe.FindSubmatch(body); m != nil {
		return strings.TrimSpace(html.UnescapeString(string(m[1])))
	}
	return ""
}

// extractMaxPage returns the highest page number referenced anywhere in the
// pagination nav (present on every page, including the last one, per the
// site's own "?page=N" links), defaulting to 1 when there is no pagination.
func extractMaxPage(body []byte) int {
	maxPage := 1
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

// cleanModelURL strips tracking query params and the old ".html" suffix
// (the site 301-redirects /models/{slug}.html to /models/{slug} now, but we
// fetch the canonical form directly rather than relying on redirects).
func cleanModelURL(u string) string {
	u = stripNATS(u)
	u = strings.TrimSuffix(u, ".html")
	return u
}

func stripNATS(u string) string {
	if idx := strings.Index(u, "?nats="); idx > 0 {
		return u[:idx]
	}
	if idx := strings.Index(u, "&nats="); idx > 0 {
		return u[:idx]
	}
	return u
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
