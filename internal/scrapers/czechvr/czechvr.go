package czechvr

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

type siteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

var sites = []siteConfig{
	{"czechvrnetwork", "czechvrnetwork.com", "CzechVR Network"},
	{"czechvr", "czechvr.com", "Czech VR"},
	{"czechvrfetish", "czechvrfetish.com", "Czech VR Fetish"},
	{"czechvrcasting", "czechvrcasting.com", "Czech VR Casting"},
	{"vrintimacy", "vrintimacy.com", "VR Intimacy"},
	{"czechar", "czechar.com", "Czech AR"},
}

type siteScraper struct {
	config  siteConfig
	matchRe *regexp.Regexp
	client  *http.Client
}

var _ scraper.StudioScraper = (*siteScraper)(nil)

func newSiteScraper(cfg siteConfig) *siteScraper {
	re := regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?%s(?:/|$)`, strings.ReplaceAll(cfg.Domain, ".", `\.`)))
	return &siteScraper{
		config:  cfg,
		matchRe: re,
		client:  httpx.NewClient(30 * time.Second),
	}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newSiteScraper(cfg))
	}
}

func (s *siteScraper) ID() string { return s.config.SiteID }

func (s *siteScraper) Patterns() []string {
	return []string{
		s.config.Domain,
		s.config.Domain + "/model-{slug}",
		s.config.Domain + "/tag-{slug}",
		s.config.Domain + "/vr-porn-videos",
	}
}

func (s *siteScraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *siteScraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type workItem struct {
	url        string
	id         string
	title      string
	performers []string
	thumb      string
	preview    string
	date       time.Time
	duration   int
}

func (s *siteScraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	delay := opts.Delay
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan workItem)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				scene, err := s.fetchDetail(ctx, item, studioURL, delay)
				if err != nil {
					select {
					case out <- scraper.Error(err):
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
		}()
	}

	s.produceListing(ctx, studioURL, opts, out, work)
	close(work)
	wg.Wait()
}

var modelPageRe = regexp.MustCompile(`/model-([a-zA-Z0-9-]+)`)

// maxModelPages bounds the model pager walk; no performer on the network is
// near this many pages.
const maxModelPages = 200

func (s *siteScraper) produceListing(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- workItem) {
	if modelPageRe.MatchString(studioURL) {
		s.scrapeModelPage(ctx, studioURL, opts, out, work)
		return
	}
	s.scrapeSitemap(ctx, opts, out, work)
}

func (s *siteScraper) siteBase() string {
	return "https://www." + s.config.Domain
}

var sitemapURLRe = regexp.MustCompile(`<loc>[^<]*/(detail-(\d+)-([^<]+))</loc>`)

// scrapeSitemap makes a single sitemap request and dispatches work items; the
// per-request delay is applied by the workers in fetchDetail, not here.
func (s *siteScraper) scrapeSitemap(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- workItem) {
	sitemapURL := s.siteBase() + "/sitemap.php"

	body, err := s.fetchPage(ctx, sitemapURL)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("sitemap: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	matches := sitemapURLRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return
	}

	select {
	case out <- scraper.Progress(len(matches)):
	case <-ctx.Done():
		return
	}

	for _, m := range matches {
		if ctx.Err() != nil {
			return
		}

		id := m[2]
		if opts.KnownIDs[id] {
			scraper.Debugf(1, "%s: hit known ID, stopping early", s.config.SiteID)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}

		item := workItem{
			url: s.siteBase() + "/" + m[1],
			id:  id,
		}
		select {
		case work <- item:
		case <-ctx.Done():
			return
		}
	}
}

var (
	cardURLRe    = regexp.MustCompile(`href="\./detail-(\d+)-([^"]+)"`)
	cardTitleRe  = regexp.MustCompile(`<h2><a[^>]+>([^<]+)</a></h2>`)
	cardActorsRe = regexp.MustCompile(`(?s)<div class="featuring">(.*?)</div>`)
	cardActorRe  = regexp.MustCompile(`>([^<]+)</a>`)
	cardDateRe   = regexp.MustCompile(`class="datum">([^<]+)<`)
	cardTimeRe   = regexp.MustCompile(`class="cas"><span[^>]*>(\d+):(\d+)</span>`)
	cardThumbSrc = regexp.MustCompile(`data-src="([^"]+)"`)
	cardVideoRe  = regexp.MustCompile(`<source src="([^"]+\.mp4[^"]*)"`)
)

// modelNextRe finds the model pager's "next" link. The pager is an item offset
// (`?&next=19`), not a page number, so the link is followed rather than
// computed. A model whose scenes fit on one page renders no pager at all.
var modelNextRe = regexp.MustCompile(`class="next" href="\./([^"]+)"`)

// scrapeModelPage walks a model's listing. It used to fetch exactly one page:
// live, Alexis Crystal's 41 scenes on czechvrnetwork arrive 24 then 17, so 41%
// of her catalogue was never seen.
func (s *siteScraper) scrapeModelPage(ctx context.Context, pageURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- workItem) {
	seen := make(map[string]bool)
	sentTotal := false

	for page := 1; page <= maxModelPages; page++ {
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "%s: fetching model page %d (%s)", s.config.SiteID, page, pageURL)

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(err):
			case <-ctx.Done():
			}
			return
		}

		items := s.parseListingCards(body)
		fresh := items[:0]
		for _, item := range items {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			fresh = append(fresh, item)
		}
		if len(fresh) == 0 {
			return
		}
		if !sentTotal {
			sentTotal = true
			select {
			case out <- scraper.Progress(len(fresh)):
			case <-ctx.Done():
				return
			}
		}

		hitKnown := false
		for _, item := range fresh {
			if opts.KnownIDs[item.id] {
				hitKnown = true
				continue
			}
			select {
			case work <- item:
			case <-ctx.Done():
				return
			}
		}
		if hitKnown {
			scraper.Debugf(1, "%s: model page %d reached stored scenes, stopping", s.config.SiteID, page)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}

		m := modelNextRe.FindStringSubmatch(body)
		if m == nil {
			return
		}
		// Resolved against the page it was found on, not the configured
		// origin, so the walk is exercisable offline.
		next, err := url.Parse(html.UnescapeString(m[1]))
		if err != nil {
			return
		}
		base, err := url.Parse(pageURL)
		if err != nil {
			return
		}
		pageURL = base.ResolveReference(next).String()
	}
}

func (s *siteScraper) parseListingCards(page string) []workItem {
	posts := findPostBlocks(page)
	seen := map[string]bool{}
	var items []workItem

	for _, block := range posts {
		m := cardURLRe.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true

		item := workItem{
			id:  id,
			url: s.siteBase() + "/detail-" + id + "-" + m[2],
		}

		if tm := cardTitleRe.FindStringSubmatch(block); tm != nil {
			item.title = html.UnescapeString(strings.TrimSpace(tm[1]))
		}

		if am := cardActorsRe.FindStringSubmatch(block); am != nil {
			for _, pm := range cardActorRe.FindAllStringSubmatch(am[1], -1) {
				item.performers = append(item.performers, strings.TrimSpace(pm[1]))
			}
		}

		if dm := cardDateRe.FindStringSubmatch(block); dm != nil {
			item.date = parseDate(strings.TrimSpace(dm[1]))
		}

		if tm := cardTimeRe.FindStringSubmatch(block); tm != nil {
			mins, _ := strconv.Atoi(tm[1])
			secs, _ := strconv.Atoi(tm[2])
			item.duration = mins*60 + secs
		}

		if tm := cardThumbSrc.FindStringSubmatch(block); tm != nil {
			item.thumb = s.resolveURL(tm[1])
		}

		if vm := cardVideoRe.FindStringSubmatch(block); vm != nil {
			item.preview = vm[1]
		}

		items = append(items, item)
	}
	return items
}

var postAnchorRe = regexp.MustCompile(`<a name="post\d+"></a>`)

func findPostBlocks(page string) []string {
	locs := postAnchorRe.FindAllStringIndex(page, -1)
	if len(locs) == 0 {
		return nil
	}

	var blocks []string
	for i, loc := range locs {
		start := loc[0]
		var end int
		if i+1 < len(locs) {
			end = locs[i+1][0]
		} else {
			end = len(page)
		}
		blocks = append(blocks, page[start:end])
	}
	return blocks
}

var (
	detailTitleRe   = regexp.MustCompile(`(?s)<h1>(.*?)</h1>`)
	detailDateRe    = regexp.MustCompile(`<div class="datum">([^<]+)</div>`)
	detailTimeRe    = regexp.MustCompile(`<div class="cas">(\d+):(\d+)</div>`)
	detailDescRe    = regexp.MustCompile(`(?s)<div class="text">\s*(.*?)\s*</div>`)
	detailActorsRe  = regexp.MustCompile(`(?s)<div class="modelky">(.*?)<div class="cistic">\s*</div>\s*</div>`)
	detailActorRe   = regexp.MustCompile(`<span>([^<]+)</span>`)
	detailTagsRe    = regexp.MustCompile(`(?s)<div id="Tagy"[^>]*>(.*?)<div class="cistic">`)
	detailTagRe     = regexp.MustCompile(`>([^<]+)</a></div>`)
	detailPosterRe  = regexp.MustCompile(`poster="([^"]+)"`)
	detailPreviewRe = regexp.MustCompile(`<source src="(https://preview\.[^"]+\.mp4[^"]*)"`)
)

func (s *siteScraper) fetchDetail(ctx context.Context, item workItem, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	body, err := s.fetchPage(ctx, item.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", item.url, err)
	}

	if item.title == "" {
		if m := detailTitleRe.FindStringSubmatch(body); m != nil {
			raw := stripTags(m[1])
			if idx := strings.Index(raw, " - "); idx >= 0 {
				raw = raw[idx+3:]
			}
			item.title = html.UnescapeString(strings.TrimSpace(raw))
		}
	}

	if item.date.IsZero() {
		if m := detailDateRe.FindStringSubmatch(body); m != nil {
			item.date = parseDate(strings.TrimSpace(m[1]))
		}
	}

	if item.duration == 0 {
		if m := detailTimeRe.FindStringSubmatch(body); m != nil {
			mins, _ := strconv.Atoi(m[1])
			secs, _ := strconv.Atoi(m[2])
			item.duration = mins*60 + secs
		}
	}

	var description string
	if m := detailDescRe.FindStringSubmatch(body); m != nil {
		description = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	if len(item.performers) == 0 {
		if am := detailActorsRe.FindStringSubmatch(body); am != nil {
			for _, pm := range detailActorRe.FindAllStringSubmatch(am[1], -1) {
				item.performers = append(item.performers, strings.TrimSpace(pm[1]))
			}
		}
	}

	var tags []string
	if tm := detailTagsRe.FindStringSubmatch(body); tm != nil {
		for _, t := range detailTagRe.FindAllStringSubmatch(tm[1], -1) {
			tags = append(tags, strings.TrimSpace(t[1]))
		}
	}

	if item.thumb == "" {
		if m := detailPosterRe.FindStringSubmatch(body); m != nil {
			item.thumb = s.resolveURL(m[1])
		}
	}

	if item.preview == "" {
		if m := detailPreviewRe.FindStringSubmatch(body); m != nil {
			item.preview = m[1]
		}
	}

	now := time.Now().UTC()
	return models.Scene{
		ID:          item.id,
		SiteID:      s.config.SiteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         item.url,
		Date:        item.date.UTC(),
		Description: description,
		Thumbnail:   item.thumb,
		Preview:     item.preview,
		Performers:  item.performers,
		Studio:      s.config.StudioName,
		Tags:        tags,
		Duration:    item.duration,
		ScrapedAt:   now,
	}, nil
}

func (s *siteScraper) fetchPage(ctx context.Context, pageURL string) (string, error) {
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

var tagStripRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string {
	return strings.TrimSpace(tagStripRe.ReplaceAllString(s, ""))
}

func (s *siteScraper) resolveURL(u string) string {
	if strings.HasPrefix(u, "http") {
		return u
	}
	if strings.HasPrefix(u, "./") {
		return s.siteBase() + u[1:]
	}
	if strings.HasPrefix(u, "/") {
		return s.siteBase() + u
	}
	return s.siteBase() + "/" + u
}

func parseDate(s string) time.Time {
	t, _ := parseutil.TryParseDate(strings.TrimSpace(s), "January 2, 2006", "Jan 2, 2006", "January 02, 2006", "Jan 02, 2006")
	return t
}
