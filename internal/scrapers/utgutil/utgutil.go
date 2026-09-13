package utgutil

import (
	"context"
	"fmt"
	"html"
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

const perPage = 200

type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	Legacy     bool // old Bootstrap 3 template (no <article> tags)
	YearBased  bool // legacy sites with /updates/videos/{YY} year pagination
}

type Scraper struct {
	client       *http.Client
	cfg          SiteConfig
	baseOverride string
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		cfg:    cfg,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) base() string {
	if s.baseOverride != "" {
		return s.baseOverride
	}
	return "https://" + s.cfg.Domain
}

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain + "/updates/videos",
		s.cfg.Domain + "/models/{slug}",
	}
}

var modelPageRe = regexp.MustCompile(`/models/([a-z0-9-]+)`)

func (s *Scraper) MatchesURL(u string) bool {
	p, err := url.Parse(u)
	if err != nil {
		return false
	}
	host := strings.TrimPrefix(p.Hostname(), "www.")
	expected := strings.TrimPrefix(s.cfg.Domain, "www.")
	if host != expected {
		return false
	}
	path := p.Path
	return path == "" || path == "/" ||
		strings.HasPrefix(path, "/updates") ||
		modelPageRe.MatchString(path)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if m := modelPageRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping model page for %s", s.cfg.SiteID, m[1])
		s.runModel(ctx, studioURL, m[1], opts, out)
		return
	}
	if s.cfg.Legacy {
		if s.cfg.YearBased {
			s.runLegacyYears(ctx, studioURL, opts, out)
		} else {
			s.runLegacyPages(ctx, studioURL, opts, out)
		}
		return
	}
	s.runPaginated(ctx, studioURL, opts, out)
}

func (s *Scraper) runPaginated(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	base := s.base()
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/updates/videos/%d/%d", base, page, perPage)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		articles := parseArticles(body, true)
		if len(articles) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			total = parseVideoCount(body)
		}

		scenes := make([]models.Scene, len(articles))
		for i, a := range articles {
			scenes[i] = a.toScene(s.cfg, studioURL, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total, Done: len(articles) < perPage}, nil
	})
}

func (s *Scraper) runModel(ctx context.Context, studioURL, slug string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	base := s.base()
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/models/%s/%d/%d", base, slug, page, perPage)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		articles := parseArticles(body, false)
		if len(articles) == 0 {
			return scraper.PageResult{}, nil
		}

		videos := filterVideos(articles)

		total := 0
		if page == 1 && len(videos) > 0 {
			total = len(videos)
		}

		scenes := make([]models.Scene, len(videos))
		for i, a := range videos {
			scenes[i] = a.toScene(s.cfg, studioURL, now)
		}
		// A page of photo-only articles filters to zero videos but the listing
		// continues; keep paginating until the page is short.
		return scraper.PageResult{Scenes: scenes, Total: total, Continue: len(articles) > 0, Done: len(articles) < perPage}, nil
	})
}

// --- Legacy template (Bootstrap 3, no <article> tags) ---

var (
	legacyYearRe  = regexp.MustCompile(`/updates/videos/(\d{2})"`)
	legacyThumbRe = regexp.MustCompile(`(?s)<img[^>]+src="([^"]*category/videos/[^"]+)"[^>]*alt="([^"]*)"`)
	legacyDurRe   = regexp.MustCompile(`(?i)(\d+:\d+)\s+[Mm]inutes`)
	legacyPageRe  = regexp.MustCompile(`/updates/videos/(\d+)"`)
)

func parseLegacyArticles(body []byte) []article {
	matches := legacyThumbRe.FindAllSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}

	text := string(body)
	var out []article
	seen := make(map[string]bool)

	for _, m := range matches {
		thumb := string(m[1])
		title := strings.TrimSpace(string(m[2]))
		if title == "" {
			continue
		}

		var slug string
		if sm := cdnSlugRe.FindStringSubmatch(thumb); sm != nil {
			slug = sm[1]
		} else {
			slug = slugify(title)
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true

		a := article{
			title:     title,
			slug:      slug,
			thumbnail: thumb,
			isVideo:   true,
		}

		thumbIdx := strings.Index(text, thumb)
		if thumbIdx >= 0 {
			end := thumbIdx + len(thumb) + 500
			if end > len(text) {
				end = len(text)
			}
			block := text[thumbIdx:end]

			if dur := legacyDurRe.FindStringSubmatch(block); dur != nil {
				a.duration = parseutil.ParseDurationColon(dur[1])
			}
		}

		out = append(out, a)
	}
	return out
}

func parseLegacyYears(body []byte) []int {
	seen := make(map[int]bool)
	var years []int
	for _, m := range legacyYearRe.FindAllSubmatch(body, -1) {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if !seen[n] {
			seen[n] = true
			years = append(years, n)
		}
	}
	// Sort descending (newest first)
	for i := 0; i < len(years); i++ {
		for j := i + 1; j < len(years); j++ {
			if years[j] > years[i] {
				years[i], years[j] = years[j], years[i]
			}
		}
	}
	return years
}

func parseLegacyMaxPage(body []byte) int {
	maxPage := 0
	for _, m := range legacyPageRe.FindAllSubmatch(body, -1) {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

func (s *Scraper) runLegacyYears(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	base := s.base()

	scraper.Debugf(1, "%s: fetching year index", s.cfg.SiteID)
	indexBody, err := s.fetchPage(ctx, base+"/updates/videos")
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	years := parseLegacyYears(indexBody)
	if len(years) == 0 {
		return
	}
	scraper.Debugf(1, "%s: %d year pages to scan", s.cfg.SiteID, len(years))

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if page > len(years) {
			return scraper.PageResult{}, nil
		}
		yr := years[page-1]

		pageURL := fmt.Sprintf("%s/updates/videos/%d", base, yr)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, fmt.Errorf("year %d: %w", yr, err)
		}

		articles := parseLegacyArticles(body)
		scenes := make([]models.Scene, len(articles))
		for i, a := range articles {
			scenes[i] = a.toScene(s.cfg, studioURL, now)
		}
		// Fixed walk over the year list: a year with no video articles must not
		// stop the walk — only exhausting the year list (Done) ends it.
		return scraper.PageResult{Scenes: scenes, Continue: true, Done: page >= len(years)}, nil
	})
}

func (s *Scraper) runLegacyPages(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	base := s.base()
	now := time.Now().UTC()

	// Probe whether /page/200 style is supported
	usePerPage := true
	probeBody, err := s.fetchPage(ctx, fmt.Sprintf("%s/updates/videos/1/200", base))
	if err != nil {
		usePerPage = false
		probeBody, err = s.fetchPage(ctx, fmt.Sprintf("%s/updates/videos/1/", base))
		if err != nil {
			probeBody, err = s.fetchPage(ctx, base+"/updates/videos")
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
		}
	}

	// Check if page 1 has content at all.
	firstArticles := parseLegacyArticles(probeBody)
	if len(firstArticles) == 0 {
		return
	}

	firstPageMaxPg := parseLegacyMaxPage(probeBody)

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		var articles []article
		var maxPg int

		if page == 1 {
			articles = firstArticles
			maxPg = firstPageMaxPg
		} else {
			var pageURL string
			if usePerPage {
				pageURL = fmt.Sprintf("%s/updates/videos/%d/200", base, page)
			} else {
				pageURL = fmt.Sprintf("%s/updates/videos/%d/", base, page)
			}
			body, err := s.fetchPage(ctx, pageURL)
			if err != nil {
				return scraper.PageResult{}, err
			}

			articles = parseLegacyArticles(body)
			if len(articles) == 0 {
				return scraper.PageResult{}, nil
			}
			maxPg = parseLegacyMaxPage(body)
		}

		total := 0
		if page == 1 {
			if maxPg > 1 {
				total = maxPg * len(articles)
			} else {
				total = len(articles)
			}
		}

		scenes := make([]models.Scene, len(articles))
		for i, a := range articles {
			scenes[i] = a.toScene(s.cfg, studioURL, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total, Done: page >= maxPg}, nil
	})
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(""),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

type article struct {
	title     string
	slug      string // from CDN path, e.g. "laura_hollyman_crimson_canvas_bts"
	thumbnail string
	date      string
	duration  int
	model     string
	modelSlug string
	isVideo   bool
}

var (
	articleRe    = regexp.MustCompile(`(?s)<article[^>]*>(.+?)</article>`)
	imgRe        = regexp.MustCompile(`<img\s[^>]*src="([^"]+)"[^>]*alt="([^"]*)"`)
	modelRe      = regexp.MustCompile(`href="/models/([^"]+)"[^>]*>([^<]+)</a>`)
	dateRe       = regexp.MustCompile(`\b(\d{1,2}\s+(?:January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{4})\b`)
	durationRe   = regexp.MustCompile(`(\d+:\d+)\s+Minutes`)
	videoBanner  = regexp.MustCompile(`update_video_banner`)
	cdnSlugRe    = regexp.MustCompile(`/videos/([^/]+)/`)
	videoCountRe = regexp.MustCompile(`HD\s*VIDEO:\s*([\d,]+)`)
)

func parseArticles(body []byte, videosOnly bool) []article {
	matches := articleRe.FindAllSubmatch(body, -1)
	var out []article
	for _, m := range matches {
		block := m[1]
		a := article{}

		if im := imgRe.FindSubmatch(block); im != nil {
			a.thumbnail = string(im[1])
			a.title = html.UnescapeString(string(im[2]))
		}
		if a.title == "" {
			continue
		}

		if mm := modelRe.FindSubmatch(block); mm != nil {
			a.modelSlug = string(mm[1])
			a.model = strings.TrimSpace(string(mm[2]))
		}

		if dm := dateRe.FindSubmatch(block); dm != nil {
			a.date = string(dm[1])
		}

		if dur := durationRe.FindSubmatch(block); dur != nil {
			a.duration = parseutil.ParseDurationColon(string(dur[1]))
			a.isVideo = true
		}

		if videoBanner.Match(block) {
			a.isVideo = true
		}

		if sm := cdnSlugRe.FindSubmatch([]byte(a.thumbnail)); sm != nil {
			a.slug = string(sm[1])
		}

		if a.slug == "" {
			a.slug = slugify(a.title)
		}

		if videosOnly && !a.isVideo {
			continue
		}
		out = append(out, a)
	}
	return out
}

func filterVideos(articles []article) []article {
	var out []article
	for _, a := range articles {
		if a.isVideo {
			out = append(out, a)
		}
	}
	return out
}

func parseVideoCount(body []byte) int {
	m := videoCountRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	s := strings.ReplaceAll(string(m[1]), ",", "")
	var n int
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

func (a article) toScene(cfg SiteConfig, studioURL string, now time.Time) models.Scene {
	thumb := a.thumbnail
	if idx := strings.Index(thumb, "?"); idx > 0 {
		thumb = thumb[:idx]
	}

	var performers []string
	if a.model != "" {
		performers = []string{a.model}
	}

	return models.Scene{
		ID:         a.slug,
		SiteID:     cfg.SiteID,
		StudioURL:  studioURL,
		Title:      a.title,
		URL:        fmt.Sprintf("https://%s/updates/previews/videos/%s", cfg.Domain, slugify(a.title)),
		Date:       parseDate(a.date),
		Duration:   a.duration,
		Performers: performers,
		Thumbnail:  thumb,
		Studio:     cfg.StudioName,
		ScrapedAt:  now,
	}
}

func parseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse("2 January 2006", s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func slugify(title string) string {
	s := strings.ToLower(title)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		if r == ' ' || r == '-' || r == '_' {
			return '-'
		}
		return -1
	}, s)
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}
