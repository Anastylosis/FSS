package wownetworkutil

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// RecommendedDelay is the pace this scraper was written against. It is **not**
// silently enforced — the operator's `opts.Delay` is always honoured, including
// `--delay 0` — but `WarnDelayBelow` surfaces a one-shot stderr warning below it.
const RecommendedDelay = 500 * time.Millisecond

type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	AltDomains []string
}

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	matchRe *regexp.Regexp
}

func New(cfg SiteConfig) *Scraper {
	domains := []string{regexp.QuoteMeta(cfg.Domain)}
	for _, d := range cfg.AltDomains {
		domains = append(domains, regexp.QuoteMeta(d))
	}
	re := regexp.MustCompile(`^https?://(?:www\.)?(?:` + strings.Join(domains, "|") + `)(?:/|$)`)
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		matchRe: re,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/tour/whats-new",
		s.cfg.Domain + "/tour/trailer/{section}/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type sitemapURLSet struct {
	URLs []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc string `xml:"loc"`
}

var trailerPathRe = regexp.MustCompile(`/tour/trailer/[^/]+/([a-z0-9-]+)$`)

// siteRoot returns the scheme://host origin of rawURL, dropping any path so the
// root sitemap can be located regardless of which sub-URL was supplied.
func siteRoot(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return strings.TrimRight(rawURL, "/")
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	delay := opts.Delay
	scraper.WarnDelayBelow(s.cfg.SiteID, opts.Delay, RecommendedDelay)

	// The sitemap lives at the site root. A studioURL with a path (e.g.
	// /tour/whats-new, /tour/trailer/{slug}) must not have "/sitemap.xml"
	// appended to the full path — that 404s. Derive the origin instead; every
	// supported URL resolves to the same full-catalogue sitemap.
	base := siteRoot(studioURL)

	scraper.Debugf(1, "%s: fetching sitemap", s.cfg.SiteID)
	urls, err := s.fetchSitemap(ctx, base+"/sitemap.xml")
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("sitemap: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	var trailerURLs []string
	for _, u := range urls {
		if trailerPathRe.MatchString(u) {
			trailerURLs = append(trailerURLs, u)
		}
	}

	if len(trailerURLs) == 0 {
		return
	}
	scraper.Debugf(1, "%s: %d total scenes from sitemap", s.cfg.SiteID, len(trailerURLs))

	select {
	case out <- scraper.Progress(len(trailerURLs)):
	case <-ctx.Done():
		return
	}

	performers := s.fetchListingPerformers(ctx, base+"/tour/whats-new")

	skipped := 0
	for i, u := range trailerURLs {
		if ctx.Err() != nil {
			return
		}

		if i > 0 && !scraper.Pace(ctx, delay) {
			return
		}

		slug := extractSlug(u)
		// The sitemap is in no order at all — wowgirls' first twelve trailers
		// run 2013, 2024, 2025, 2026, 2014, 2024, 2012, 2023 — and it carries
		// no <lastmod> or any other date to sort on; the date only exists on
		// the detail page, which is the very fetch an early stop exists to
		// avoid. So a stop at the first stored slug would abort at an
		// arbitrary position and permanently miss everything after it. Stored
		// scenes are skipped instead, which costs nothing on a list this size.
		if opts.KnownIDs[slug] {
			skipped++
			continue
		}

		scene, err := s.fetchDetail(ctx, u, studioURL, performers)
		if err != nil {
			// One unreachable trailer is not the catalogue. Report it and keep
			// going: returning here handed an authoritative --full save every
			// scene up to the first failure and nothing after it.
			select {
			case out <- scraper.Error(fmt.Errorf("%s: %w", slug, err)):
			case <-ctx.Done():
				return
			}
			continue
		}

		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}

	if skipped > 0 {
		scraper.Debugf(1, "%s: skipped %d already-stored scene(s)", s.cfg.SiteID, skipped)
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
}

func (s *Scraper) fetchSitemap(ctx context.Context, sitemapURL string) ([]string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     sitemapURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}

	var urlset sitemapURLSet
	if err := xml.Unmarshal(body, &urlset); err != nil {
		return nil, fmt.Errorf("parsing sitemap: %w", err)
	}

	var urls []string
	for _, u := range urlset.URLs {
		urls = append(urls, u.Loc)
	}
	return urls, nil
}

func (s *Scraper) fetchDetail(ctx context.Context, pageURL, studioURL string, listingPerformers map[string][]string) (models.Scene, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return models.Scene{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return models.Scene{}, err
	}

	vo := parseutil.ExtractVideoObject(body)
	if vo == nil {
		return models.Scene{}, fmt.Errorf("no JSON-LD found")
	}

	slug := extractSlug(pageURL)

	var date time.Time
	if vo.UploadDate != "" {
		date, _ = time.Parse("2006-01-02", vo.UploadDate)
	}

	performers := parseDescriptionPerformers(vo.Description)
	if len(performers) == 0 {
		performers = listingPerformers[slug]
	}

	return models.Scene{
		ID:         slug,
		SiteID:     s.cfg.SiteID,
		StudioURL:  studioURL,
		Title:      vo.Name,
		URL:        pageURL,
		Date:       date.UTC(),
		Duration:   parseutil.ParseDurationISO(vo.Duration),
		Thumbnail:  vo.ThumbnailURL,
		Performers: performers,
		Studio:     s.cfg.StudioName,
		ScrapedAt:  time.Now().UTC(),
	}, nil
}

var performerTagRe = regexp.MustCompile(`<a[^>]*data-property="model"[^>]*>([^<]+)</a>`)

func parseDescriptionPerformers(desc string) []string {
	matches := performerTagRe.FindAllStringSubmatch(desc, -1)
	if len(matches) == 0 {
		return nil
	}
	var performers []string
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		if name != "" {
			performers = append(performers, name)
		}
	}
	return performers
}

var listingCardRe = regexp.MustCompile(`(?s)data-href="/tour/trailer/[^"]*?/([a-z0-9-]+)".*?(?:</div>\s*</div>\s*</div>)`)
var listingModelRe = regexp.MustCompile(`class="model meta-list-item">([^<]+)`)

func (s *Scraper) fetchListingPerformers(ctx context.Context, listingURL string) map[string][]string {
	result := make(map[string][]string)

	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     listingURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return result
	}

	parseListingPerformers(body, result)
	return result
}

func parseListingPerformers(body []byte, result map[string][]string) {
	cards := listingCardRe.FindAllSubmatch(body, -1)
	for _, card := range cards {
		slug := string(card[1])
		models := listingModelRe.FindAllSubmatch(card[0], -1)
		var performers []string
		for _, m := range models {
			name := strings.TrimSpace(string(m[1]))
			if name != "" {
				performers = append(performers, name)
			}
		}
		if len(performers) > 0 {
			result[slug] = performers
		}
	}
}

func extractSlug(u string) string {
	m := trailerPathRe.FindStringSubmatch(u)
	if m == nil {
		i := strings.LastIndex(u, "/")
		if i >= 0 {
			return u[i+1:]
		}
		return u
	}
	return m[1]
}
