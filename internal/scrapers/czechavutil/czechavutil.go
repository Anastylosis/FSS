package czechavutil

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	SiteID string
	Domain string
	Studio string
}

type Scraper struct {
	cfg     SiteConfig
	Client  *http.Client
	Base    string
	matchRe *regexp.Regexp
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		Base:    "https://" + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }
func (s *Scraper) MatchesURL(u string) bool {
	return s.matchRe.MatchString(u)
}
func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/video/{slug}",
		s.cfg.Domain + "/pages/search/?q={name}&adult-performer",
	}
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type sitemapURL struct {
	Loc     string       `xml:"loc"`
	LastMod string       `xml:"lastmod"`
	Video   sitemapVideo `xml:"http://www.google.com/schemas/sitemap-video/1.1 video"`
}

type sitemapVideo struct {
	Title       string  `xml:"http://www.google.com/schemas/sitemap-video/1.1 title"`
	Description string  `xml:"http://www.google.com/schemas/sitemap-video/1.1 description"`
	Thumbnail   string  `xml:"http://www.google.com/schemas/sitemap-video/1.1 thumbnail_loc"`
	Duration    float64 `xml:"http://www.google.com/schemas/sitemap-video/1.1 duration"`
	PubDate     string  `xml:"http://www.google.com/schemas/sitemap-video/1.1 publication_date"`
}

type sitemapIndex struct {
	XMLName xml.Name     `xml:"urlset"`
	URLs    []sitemapURL `xml:"url"`
}

func ParseSitemap(body []byte) []sitemapURL {
	body = sanitizeXML(body)
	var idx sitemapIndex
	if err := xml.Unmarshal(body, &idx); err != nil {
		return nil
	}
	return idx.URLs
}

func sanitizeXML(data []byte) []byte {
	clean := make([]byte, 0, len(data))
	for _, b := range data {
		if b == 0x09 || b == 0x0A || b == 0x0D || b >= 0x20 {
			clean = append(clean, b)
		}
	}
	return clean
}

func ExtractSlug(loc, domain string) string {
	for _, scheme := range []string{"https://", "http://"} {
		prefix := scheme + domain + "/sitemap.xml/video/"
		if strings.HasPrefix(loc, prefix) {
			return strings.TrimSuffix(strings.TrimPrefix(loc, prefix), "/")
		}
		prefix2 := scheme + domain + "/video/"
		if strings.HasPrefix(loc, prefix2) {
			return strings.TrimSuffix(strings.TrimPrefix(loc, prefix2), "/")
		}
	}
	return ""
}

type DetailData struct {
	Performers []string
	Tags       []string
}

var performerRe = regexp.MustCompile(`[?&]adult-performer[^"]*"[^>]*class="[^"]*text-link[^"]*"[^>]*>([^<]+)</a>`)

func ParseDetailPage(body []byte) DetailData {
	var d DetailData

	if vo := parseutil.ExtractVideoObject(body); vo != nil {
		d.Performers = vo.Actors
		if vo.Keywords != "" {
			for _, kw := range strings.Split(vo.Keywords, ",") {
				tag := strings.TrimSpace(kw)
				if tag != "" {
					d.Tags = append(d.Tags, tag)
				}
			}
		}
	}

	if len(d.Performers) == 0 {
		for _, m := range performerRe.FindAllSubmatch(body, -1) {
			name := strings.TrimSpace(html.UnescapeString(string(m[1])))
			if name != "" {
				d.Performers = appendUnique(d.Performers, name)
			}
		}
	}

	return d
}

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if strings.EqualFold(s, val) {
			return slice
		}
	}
	return append(slice, val)
}

var searchPerformerRe = regexp.MustCompile(`/pages/search/\?q=([^&#]+)&adult-performer`)
var searchVideoSlugRe = regexp.MustCompile(`href="/video/([^/"]+)/"`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if searchPerformerRe.MatchString(studioURL) {
		scraper.Debugf(1, "%s: detected performer search page", s.cfg.SiteID)
		s.scrapePerformerSearch(ctx, studioURL, opts, out)
		return
	}

	s.scrapeSitemap(ctx, studioURL, opts, out)
}

func (s *Scraper) scrapePerformerSearch(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	searchURL := studioURL
	if !strings.HasPrefix(searchURL, "http") {
		searchURL = s.Base + searchURL
	}

	body, err := s.fetchPage(ctx, searchURL)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("search page: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	seen := map[string]bool{}
	var slugs []string
	for _, m := range searchVideoSlugRe.FindAllSubmatch(body, -1) {
		slug := string(m[1])
		if !seen[slug] {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
	}

	if len(slugs) == 0 {
		return
	}
	scraper.Debugf(1, "%s: found %d scenes on performer page", s.cfg.SiteID, len(slugs))

	select {
	case out <- scraper.Progress(len(slugs)):
	case <-ctx.Done():
		return
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan string, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for slug := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, ferr := s.fetchSceneBySlug(ctx, slug, studioURL)
				if ferr != nil {
					select {
					case out <- scraper.Error(ferr):
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

	// A performer page is a single page that has already been fetched in full,
	// and its scenes are in no particular order, so stopping at the first
	// stored slug saves nothing and would drop every scene listed after it.
	// Known slugs are skipped instead.
	skipped := 0
	for _, slug := range slugs {
		if opts.KnownIDs[slug] {
			skipped++
			continue
		}
		select {
		case work <- slug:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()

	if skipped > 0 {
		scraper.Debugf(1, "%s: skipped %d already-stored scene(s)", s.cfg.SiteID, skipped)
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
}

// sceneEntry pairs a sitemap URL with the slug derived from it.
type sceneEntry struct {
	slug string
	url  sitemapURL
}

// sortNewestFirst orders sitemap entries by publication date, newest first.
// Entries whose date is missing or unparseable sort ahead of everything dated,
// so the KnownIDs stop can never skip past one.
func sortNewestFirst(entries []sceneEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		di, oki := pubDate(entries[i])
		dj, okj := pubDate(entries[j])
		if oki != okj {
			return !oki
		}
		return di.After(dj)
	})
}

func pubDate(e sceneEntry) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(e.url.Video.PubDate))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func (s *Scraper) fetchSceneBySlug(ctx context.Context, slug, studioURL string) (models.Scene, error) {
	sceneURL := s.Base + "/video/" + slug + "/"
	detailBody, err := s.fetchPage(ctx, sceneURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", slug, err)
	}

	detail := ParseDetailPage(detailBody)
	now := time.Now().UTC()

	sc := models.Scene{
		ID:         slug,
		SiteID:     s.cfg.SiteID,
		StudioURL:  studioURL,
		URL:        sceneURL,
		Performers: detail.Performers,
		Tags:       detail.Tags,
		Studio:     s.cfg.Studio,
		ScrapedAt:  now,
	}

	if vo := parseutil.ExtractVideoObject(detailBody); vo != nil {
		sc.Title = vo.Name
		sc.Description = vo.Description
		sc.Thumbnail = vo.ThumbnailURL
		sc.Duration = parseutil.ParseDurationISO(vo.Duration)
		if vo.UploadDate != "" {
			if t, err := time.Parse("2006-01-02", vo.UploadDate); err == nil {
				sc.Date = t.UTC()
			}
		}
	}

	return sc, nil
}

func (s *Scraper) scrapeSitemap(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	body, err := s.fetchPage(ctx, s.Base+"/sitemap.xml")
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("sitemap: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	urls := ParseSitemap(body)
	if len(urls) == 0 {
		select {
		case out <- scraper.Error(fmt.Errorf("no scenes in sitemap")):
		case <-ctx.Done():
		}
		return
	}

	var entries []sceneEntry
	for _, u := range urls {
		slug := ExtractSlug(u.Loc, s.cfg.Domain)
		if slug == "" || u.Video.Title == "" {
			continue
		}
		entries = append(entries, sceneEntry{slug: slug, url: u})
	}

	if len(entries) == 0 {
		return
	}

	// The sitemap is in no order at all — czechcasting's runs 2011-11-22,
	// 2011-11-05, 2012-01-09 … 2026-04-28, 2026-08-17, 2024-10-08 — so the
	// KnownIDs stop below would abort at whatever position the first stored
	// scene happened to occupy and permanently miss everything after it.
	// Sorting on the sitemap's own publication_date makes the stop mean what
	// it says. Undated entries sort first so a stop can never skip them.
	sortNewestFirst(entries)

	select {
	case out <- scraper.Progress(len(entries)):
	case <-ctx.Done():
		return
	}

	work := make(chan sceneEntry, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, ferr := s.fetchScene(ctx, entry.slug, entry.url, studioURL)
				if ferr != nil {
					select {
					case out <- scraper.Error(ferr):
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

	cancelled := false
	for _, entry := range entries {
		if opts.KnownIDs[entry.slug] {
			scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			break
		}
		select {
		case work <- entry:
		case <-ctx.Done():
			cancelled = true
		}
		if cancelled {
			break
		}
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) fetchScene(ctx context.Context, slug string, u sitemapURL, studioURL string) (models.Scene, error) {
	sceneURL := s.Base + "/video/" + slug + "/"

	detailBody, err := s.fetchPage(ctx, sceneURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", slug, err)
	}

	detail := ParseDetailPage(detailBody)

	var date time.Time
	if u.Video.PubDate != "" {
		if t, err := time.Parse("2006-01-02", u.Video.PubDate); err == nil {
			date = t.UTC()
		}
	} else if u.LastMod != "" {
		if t, err := time.Parse("2006-01-02", u.LastMod); err == nil {
			date = t.UTC()
		}
	}

	now := time.Now().UTC()
	return models.Scene{
		ID:          slug,
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		URL:         sceneURL,
		Title:       strings.TrimSpace(u.Video.Title),
		Description: strings.TrimSpace(u.Video.Description),
		Thumbnail:   u.Video.Thumbnail,
		Duration:    int(u.Video.Duration),
		Date:        date,
		Performers:  detail.Performers,
		Tags:        detail.Tags,
		Studio:      s.cfg.Studio,
		ScrapedAt:   now,
	}, nil
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
