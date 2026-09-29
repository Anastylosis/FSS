package virtualtaboo

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
	"github.com/Anastylosis/FSS/scraper"
)

type siteConfig struct {
	siteID     string
	domain     string
	studioName string
	listPath   string
}

var sites = []siteConfig{
	{"darkroomvr", "darkroomvr.com", "Dark Room VR", "/video"},
	{"onlytarts", "onlytarts.com", "OnlyTarts", "/video"},
	{"virtualtaboo", "virtualtaboo.com", "Virtual Taboo", "/videos"},
}

type Scraper struct {
	cfg          siteConfig
	client       *http.Client
	baseOverride string
	matchRe      *regexp.Regexp
}

func init() {
	for _, cfg := range sites {
		escaped := strings.ReplaceAll(cfg.domain, ".", `\.`)
		s := &Scraper{
			cfg:     cfg,
			client:  httpx.NewClient(30 * time.Second),
			matchRe: regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?%s(?:/|$)`, escaped)),
		}
		scraper.Register(s)
	}
}

func (s *Scraper) ID() string { return s.cfg.siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.domain + s.cfg.listPath,
		s.cfg.domain + "/pornstars/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) base() string {
	if s.baseOverride != "" {
		return s.baseOverride
	}
	return "https://" + s.cfg.domain
}

type listEntry struct {
	slug       string
	url        string
	title      string
	performers []string
	thumbnail  string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	// The live site uses /pornstars/{slug} for performer pages; /model/ is kept
	// for backward compatibility with older stored URLs.
	if strings.Contains(studioURL, "/pornstars/") || strings.Contains(studioURL, "/model/") {
		scraper.Debugf(1, "%s: scraping performer page", s.cfg.siteID)
		s.runModelPage(ctx, studioURL, opts, out)
		return
	}
	scraper.WarnURLFallthrough(s.cfg.siteID, studioURL)
	s.runPaginated(ctx, opts, out)
}

func (s *Scraper) runModelPage(ctx context.Context, modelURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetchHTML(ctx, modelURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	entries := s.parseListingEntries(body)
	if len(entries) == 0 {
		return
	}

	scraper.Debugf(1, "%s: %d total scenes", s.cfg.siteID, len(entries))
	select {
	case out <- scraper.Progress(len(entries)):
	case <-ctx.Done():
		return
	}

	s.processEntries(ctx, entries, opts, out)
}

func (s *Scraper) runPaginated(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	work := make(chan listEntry, opts.Workers)
	var wg sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, entry)
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

	sentTotal := false
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			break
		}

		scraper.Debugf(1, "%s: fetching page %d", s.cfg.siteID, page)
		pageURL := fmt.Sprintf("%s%s?page=%d", s.base(), s.cfg.listPath, page)
		body, err := s.fetchHTML(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		entries := s.parseListingEntries(body)
		if len(entries) == 0 {
			break
		}

		if !sentTotal {
			sentTotal = true
			maxPage := parseMaxPage(body)
			if maxPage > 0 {
				select {
				case out <- scraper.Progress(maxPage * len(entries)):
				case <-ctx.Done():
				}
			}
		}

		cancelled := false
		hitKnown := false
		for _, e := range entries {
			if opts.KnownIDs[e.slug] {
				hitKnown = true
				break
			}
			select {
			case work <- e:
			case <-ctx.Done():
				cancelled = true
			}
			if cancelled {
				break
			}
		}
		if cancelled || hitKnown {
			if hitKnown {
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
			}
			break
		}

		if !hasNextPage(body, page) {
			break
		}
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) processEntries(ctx context.Context, entries []listEntry, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	work := make(chan listEntry, opts.Workers)
	var wg sync.WaitGroup
	// LIFO: close(work) fires first so workers' `for ... range work` exits,
	// then wg.Wait() blocks until they're all gone. Guarantees no leak even
	// when the entry-feed loop below bails on ctx.Done.
	defer wg.Wait()
	defer close(work)
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, entry)
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

	for _, e := range entries {
		if opts.KnownIDs[e.slug] {
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			break
		}
		select {
		case work <- e:
		case <-ctx.Done():
			return
		}
	}
}

var (
	cardRe      = regexp.MustCompile(`(?s)video-card__item[^>]*>\s*<a class="image-container" href="([^"]+)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<div class="video-card__title">\s*(.*?)\s*</div>`)
	cardActorRe = regexp.MustCompile(`<a href="[^"]*/(?:model|pornstars)/[^"]*">([^<]+)</a>`)
	cardThumbRe = regexp.MustCompile(`<img src="([^"]+)"`)
	pageRe      = regexp.MustCompile(`[?&]page=(\d+)`)
)

func (s *Scraper) parseListingEntries(body []byte) []listEntry {
	page := string(body)
	var entries []listEntry
	seen := make(map[string]bool)
	base := s.base()

	parts := strings.Split(page, `video-card__item`)
	for _, part := range parts[1:] {
		m := cardRe.FindStringSubmatch("video-card__item" + part)
		if m == nil {
			continue
		}

		videoURL := m[1]
		slug := extractSlug(videoURL)
		if slug == "" || slug == "best" || seen[slug] {
			continue
		}
		seen[slug] = true

		if !strings.HasPrefix(videoURL, "http") {
			videoURL = base + videoURL
		}

		e := listEntry{
			slug: slug,
			url:  videoURL,
		}

		if tm := cardTitleRe.FindStringSubmatch(part); tm != nil {
			e.title = strings.TrimSpace(html.UnescapeString(tm[1]))
		}

		for _, am := range cardActorRe.FindAllStringSubmatch(part, -1) {
			name := strings.TrimSpace(html.UnescapeString(am[1]))
			if name != "" {
				e.performers = append(e.performers, name)
			}
		}

		if tm := cardThumbRe.FindStringSubmatch(part); tm != nil {
			e.thumbnail = html.UnescapeString(tm[1])
		}

		entries = append(entries, e)
	}

	return entries
}

var slugRe = regexp.MustCompile(`/videos?/(.+?)(?:\?|$)`)

func extractSlug(rawURL string) string {
	m := slugRe.FindStringSubmatch(rawURL)
	if m == nil {
		return ""
	}
	return m[1]
}

func parseMaxPage(body []byte) int {
	maxPage := 0
	for _, m := range pageRe.FindAllSubmatch(body, -1) {
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

func hasNextPage(body []byte, current int) bool {
	for _, m := range pageRe.FindAllSubmatch(body, -1) {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if n > current {
			return true
		}
	}
	return false
}

var (
	jsonLDRe    = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	jldNameRe   = regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)
	jldDescRe   = regexp.MustCompile(`"description"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	jldDateRe   = regexp.MustCompile(`"uploadDate"\s*:\s*"(\d{4}-\d{2}-\d{2})`)
	jldDurRe    = regexp.MustCompile(`"duration"\s*:\s*"T(\d+)H(\d+)M(\d+)S"|"duration"\s*:\s*"T(\d+)M(\d+)S"|"duration"\s*:\s*"T(\d+)M`)
	jldThumbRe  = regexp.MustCompile(`"thumbnailUrl"\s*:\s*"([^"]+)"`)
	tagLinkRe   = regexp.MustCompile(`/tag/[^"]*">([^<]+)</a>`)
	modelLinkRe = regexp.MustCompile(`/(?:model|pornstars)/[^"]*">([^<\n]+)</a>`)
)

func (s *Scraper) fetchDetail(ctx context.Context, entry listEntry) (models.Scene, error) {
	body, err := s.fetchHTML(ctx, entry.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", entry.slug, err)
	}

	return s.parseDetail(body, entry), nil
}

func (s *Scraper) parseDetail(body []byte, entry listEntry) models.Scene {
	now := time.Now().UTC()
	scene := models.Scene{
		ID:         entry.slug,
		SiteID:     s.cfg.siteID,
		StudioURL:  s.base(),
		Title:      entry.title,
		URL:        entry.url,
		Thumbnail:  entry.thumbnail,
		Performers: entry.performers,
		Studio:     s.cfg.studioName,
		ScrapedAt:  now,
	}

	jld := jsonLDRe.FindSubmatch(body)
	if jld != nil {
		block := string(jld[1])

		if scene.Title == "" {
			if m := jldNameRe.FindStringSubmatch(block); m != nil {
				scene.Title = html.UnescapeString(m[1])
			}
		}

		if m := jldDescRe.FindStringSubmatch(block); m != nil {
			raw := m[1]
			raw = strings.ReplaceAll(raw, `\"`, `"`)
			raw = strings.ReplaceAll(raw, `\\`, `\`)
			scene.Description = strings.TrimSpace(html.UnescapeString(raw))
		}

		if m := jldDateRe.FindStringSubmatch(block); m != nil {
			if t, err := time.Parse("2006-01-02", m[1]); err == nil {
				scene.Date = t.UTC()
			}
		}

		if m := jldDurRe.FindStringSubmatch(block); m != nil {
			scene.Duration = parseDuration(m)
		}

		if scene.Thumbnail == "" {
			if m := jldThumbRe.FindStringSubmatch(block); m != nil {
				scene.Thumbnail = m[1]
			}
		}
	}

	if len(scene.Performers) == 0 {
		for _, m := range modelLinkRe.FindAllSubmatch(body, -1) {
			name := strings.TrimSpace(html.UnescapeString(string(m[1])))
			if name != "" {
				scene.Performers = appendUnique(scene.Performers, name)
			}
		}
	}

	for _, m := range tagLinkRe.FindAllSubmatch(body, -1) {
		tag := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if tag != "" {
			scene.Tags = append(scene.Tags, tag)
		}
	}

	return scene
}

func parseDuration(m []string) int {
	if m[1] != "" {
		h := atoi(m[1])
		mins := atoi(m[2])
		sec := atoi(m[3])
		return h*3600 + mins*60 + sec
	}
	if m[4] != "" {
		mins := atoi(m[4])
		sec := atoi(m[5])
		return mins*60 + sec
	}
	if m[6] != "" {
		return atoi(m[6]) * 60
	}
	return 0
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

func (s *Scraper) fetchHTML(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
