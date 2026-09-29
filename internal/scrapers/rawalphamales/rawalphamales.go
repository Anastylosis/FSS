package rawalphamales

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

type Scraper struct {
	client       *http.Client
	baseOverride string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "rawalphamales" }

func (s *Scraper) Patterns() []string {
	return []string{
		"rawalphamales.com",
		"rawalphamales.com/category/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?rawalphamales\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

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
	return "https://www.rawalphamales.com"
}

type listEntry struct {
	slug      string
	url       string
	title     string
	date      string
	thumbnail string
}

var categoryPathRe = regexp.MustCompile(`/category/([^/]+)`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	var pathPrefix string
	if m := categoryPathRe.FindStringSubmatch(studioURL); m != nil {
		pathPrefix = "/category/" + m[1]
	}

	s.runPaginated(ctx, pathPrefix, opts, out)
}

func (s *Scraper) runPaginated(ctx context.Context, pathPrefix string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
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
		scraper.Debugf(1, "rawalphamales: fetching page %d", page)

		pageURL := fmt.Sprintf("%s%s/page/%d/", s.base(), pathPrefix, page)
		body, err := s.fetchHTML(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		entries := parseListingEntries(body, s.base())
		if len(entries) == 0 {
			break
		}

		if !sentTotal {
			sentTotal = true
			total := parseTotalPages(body)
			if total > 0 {
				select {
				case out <- scraper.Progress(total * len(entries)):
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
				scraper.Debugf(1, "rawalphamales: hit known ID, stopping early")
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

var (
	cardRe       = regexp.MustCompile(`(?s)<div class="video-container (?:post-content|featured-video)">\s*<h[13]>\s*<small>([^<]+)</small>\s*<a href="([^"]+)"[^>]*>([^<]+)</a>`)
	sceneThumbRe = regexp.MustCompile(`class="lazy sceneimage0" data-original="([^"]+)"`)
	featThumbRe  = regexp.MustCompile(`class="lazy a1" data-original="([^"]+)"`)
	totalPagesRe = regexp.MustCompile(`Page \d+ of (\d+)`)
	nextPageRe   = regexp.MustCompile(`class="nextpostslink"`)
	slugRe       = regexp.MustCompile(`/video/([^/]+)`)
)

func parseListingEntries(body []byte, base string) []listEntry {
	page := string(body)
	var entries []listEntry
	seen := make(map[string]bool)

	parts := strings.Split(page, `<div class="video-container `)
	for _, part := range parts[1:] {
		full := `<div class="video-container ` + part
		m := cardRe.FindStringSubmatch(full)
		if m == nil {
			continue
		}

		dateStr := strings.TrimSpace(m[1])
		videoURL := m[2]
		title := strings.TrimSpace(html.UnescapeString(m[3]))

		slug := extractSlug(videoURL)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true

		if !strings.HasPrefix(videoURL, "http") {
			videoURL = base + videoURL
		}

		e := listEntry{
			slug:  slug,
			url:   videoURL,
			title: title,
			date:  dateStr,
		}

		if tm := sceneThumbRe.FindStringSubmatch(part); tm != nil {
			thumb := tm[1]
			if !strings.HasPrefix(thumb, "http") {
				thumb = base + thumb
			}
			e.thumbnail = thumb
		} else if tm := featThumbRe.FindStringSubmatch(part); tm != nil {
			thumb := tm[1]
			if !strings.HasPrefix(thumb, "http") {
				thumb = base + thumb
			}
			e.thumbnail = thumb
		}

		entries = append(entries, e)
	}

	return entries
}

func extractSlug(rawURL string) string {
	m := slugRe.FindStringSubmatch(rawURL)
	if m == nil {
		return ""
	}
	return m[1]
}

func parseTotalPages(body []byte) int {
	m := totalPagesRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	n := 0
	for _, c := range m[1] {
		n = n*10 + int(c-'0')
	}
	return n
}

func hasNextPage(body []byte, _ int) bool {
	return nextPageRe.Match(body)
}

var (
	descRe    = regexp.MustCompile(`(?s)<p class="p1">(.*?)</p>`)
	sectionRe = regexp.MustCompile(`<meta property="article:section" content="([^"]+)"`)
	datePubRe = regexp.MustCompile(`"datePublished":"(\d{4}-\d{2}-\d{2})`)
)

func (s *Scraper) fetchDetail(ctx context.Context, entry listEntry) (models.Scene, error) {
	body, err := s.fetchHTML(ctx, entry.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", entry.slug, err)
	}

	return parseDetail(body, entry, s.base()), nil
}

func parseOrdinalDate(s string) time.Time {
	cleaned := strings.NewReplacer("st,", ",", "nd,", ",", "rd,", ",", "th,", ",").Replace(s)
	t, err := time.Parse("Jan 2, 2006", cleaned)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func parseDetail(body []byte, entry listEntry, _ string) models.Scene {
	now := time.Now().UTC()
	scene := models.Scene{
		ID:         entry.slug,
		SiteID:     "rawalphamales",
		StudioURL:  "https://www.rawalphamales.com",
		Title:      entry.title,
		URL:        entry.url,
		Thumbnail:  entry.thumbnail,
		Performers: extractPerformers(entry.title),
		Studio:     "Raw Alpha Males",
		ScrapedAt:  now,
	}

	if m := datePubRe.FindSubmatch(body); m != nil {
		if t, err := time.Parse("2006-01-02", string(m[1])); err == nil {
			scene.Date = t.UTC()
		}
	} else if entry.date != "" {
		scene.Date = parseOrdinalDate(entry.date)
	}

	if m := descRe.FindSubmatch(body); m != nil {
		scene.Description = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}

	if m := sectionRe.FindSubmatch(body); m != nil {
		tag := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if tag != "" {
			scene.Tags = append(scene.Tags, tag)
		}
	}

	return scene
}

var splitWords = map[string]bool{
	"tops": true, "bottoms": true, "fucks": true, "rides": true,
	"pounds": true, "breeds": true, "sucks": true, "services": true,
	"drills": true, "dominates": true, "owns": true, "destroys": true,
	"wrecks": true, "slams": true, "takes": true, "gets": true,
	"goes": true, "has": true, "and": true, "gives": true,
	"barebacks": true, "raw-fucks": true, "flip-fucks": true,
	"service": true, "his": true, "her": true, "their": true,
	"with": true, "in": true, "on": true, "at": true, "the": true,
	"a": true, "an": true, "to": true, "for": true, "from": true,
	"is": true, "are": true, "by": true, "of": true, "or": true,
	"big": true, "huge": true, "deep": true, "raw": true, "hard": true,
	"balls": true, "cock": true, "ass": true, "hole": true, "dick": true,
	"uncut": true, "inch": true, "double": true, "bareback": true,
	"submits": true, "plows": true, "bottom": true, "twink": true,
	"fully": true, "full": true, "edited": true,
}

func extractPerformers(title string) []string {
	title = strings.SplitN(title, "|", 2)[0]
	words := strings.Fields(title)
	var names []string
	var current []string

	flush := func() {
		if len(current) >= 1 {
			names = append(names, strings.Join(current, " "))
		}
		current = nil
	}

	for _, w := range words {
		w = stripPossessive(w)
		lower := strings.ToLower(w)
		if splitWords[lower] {
			flush()
			continue
		}
		if strings.ContainsAny(w, "0123456789-") {
			flush()
			continue
		}
		if isNameWord(w) {
			current = append(current, w)
		} else {
			flush()
		}
	}
	flush()

	var result []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n != "" && len(strings.Fields(n)) <= 4 {
			result = append(result, n)
		}
	}
	return result
}

func stripPossessive(w string) string {
	if len(w) >= 3 && w[len(w)-1] == 's' {
		if w[len(w)-2] == '\'' {
			return w[:len(w)-2]
		}
		if len(w) >= 5 && w[len(w)-4] == '\xe2' && w[len(w)-3] == '\x80' && w[len(w)-2] == '\x99' {
			return w[:len(w)-4]
		}
	}
	return w
}

func isNameWord(w string) bool {
	if len(w) == 0 {
		return false
	}
	r := rune(w[0])
	return r >= 'A' && r <= 'Z'
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
