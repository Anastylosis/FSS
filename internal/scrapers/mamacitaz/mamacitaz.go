// Package mamacitaz scrapes the MamacitaZ network (mamacitaz.com) — Chicas
// Loca, Carne Del Mercado, Her Big Ass and Operación Limpieza are channels on
// one tour, not separate sites. See docs/scrapers.md.
package mamacitaz

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
	siteID         = "mamacitaz"
	studioName     = "MamacitaZ"
	siteBase       = "https://mamacitaz.com"
	defaultWorkers = 4
)

var (
	// The network's other domains serve the same tour, so they match too.
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:mamacitaz|chicasloca|carnedelmercado|herbigass|operacionlimpieza)\.com(?:/|$)`)
	// watchRe pulls scene ids and slugs out of any listing.
	watchRe    = regexp.MustCompile(`/watch/(\d+)/([a-z0-9-]+)\.en\.html`)
	channelRe  = regexp.MustCompile(`/channels/([a-z0-9-]+)\.en\.html`)
	titleRe    = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	itempropRe = func(name string) *regexp.Regexp {
		return regexp.MustCompile(`<meta itemprop="` + name + `"[^>]*content="([^"]*)"`)
	}
	durationRe = itempropRe("duration")
	uploadRe   = itempropRe("uploadDate")
	descRe     = itempropRe("description")
	ogImageRe  = regexp.MustCompile(`<meta property="og:image" content="([^"]*)"`)
	modelRe    = regexp.MustCompile(`/models/([a-z0-9-]+)\.en\.html"[^>]*>\s*([^<]{2,40}?)\s*</a>`)
	chanLinkRe = regexp.MustCompile(`/channels/([a-z0-9-]+)\.en\.html"[^>]*>\s*([^<]{2,40}?)\s*</a>`)
	tagRe      = regexp.MustCompile(`/tags/([a-z0-9-]+)\.en\.html`)
	scriptRe   = regexp.MustCompile(`(?s)<script.*?</script>`)
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"mamacitaz.com",
		"mamacitaz.com/videos.en.html",
		"mamacitaz.com/channels/{channel}.en.html",
		"chicasloca.com",
		"carnedelmercado.com",
		"herbigass.com",
		"operacionlimpieza.com",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	listPath := "/videos.en.html"
	if m := channelRe.FindStringSubmatch(studioURL); m != nil {
		listPath = "/channels/" + m[1] + ".en.html"
		scraper.Debugf(1, "mamacitaz: scraping channel %s", m[1])
	} else if ch := channelForDomain(studioURL); ch != "" {
		listPath = "/channels/" + ch + ".en.html"
		scraper.Debugf(1, "mamacitaz: %s is the %s channel", studioURL, ch)
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?order=-recent&page=%d", s.base, listPath, page)
		refs, err := s.fetchRefs(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if len(refs) == 0 {
			return scraper.PageResult{}, nil
		}
		return scraper.PageResult{Scenes: s.fetchDetails(ctx, refs, studioURL, workers, opts.Delay, now, out)}, nil
	})
}

// channelForDomain maps a sister domain to the channel it became on the hub.
func channelForDomain(studioURL string) string {
	for domain, channel := range map[string]string{
		"chicasloca.com":        "chicas-loca",
		"carnedelmercado.com":   "carne-del-mercado",
		"herbigass.com":         "her-big-ass",
		"operacionlimpieza.com": "operacion-limpieza",
	} {
		if strings.Contains(studioURL, domain) {
			return channel
		}
	}
	return ""
}

type sceneRef struct{ id, slug string }

func (s *Scraper) fetchRefs(ctx context.Context, pageURL string) ([]sceneRef, error) {
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var refs []sceneRef
	for _, m := range watchRe.FindAllStringSubmatch(body, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		refs = append(refs, sceneRef{id: m[1], slug: m[2]})
	}
	return refs, nil
}

func (s *Scraper) fetchDetails(ctx context.Context, refs []sceneRef, studioURL string, workers int, delay time.Duration, now time.Time, out chan<- scraper.SceneResult) []models.Scene {
	results := make([]*models.Scene, len(refs))
	work := make(chan int)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range work {
				if !scraper.Pace(ctx, delay) {
					return
				}
				scene, err := s.fetchScene(ctx, refs[idx], studioURL, now)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				results[idx] = scene
			}
		}()
	}
	for i := range refs {
		select {
		case work <- i:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return nil
		}
	}
	close(work)
	wg.Wait()

	scenes := make([]models.Scene, 0, len(refs))
	for _, sc := range results {
		if sc != nil {
			scenes = append(scenes, *sc)
		}
	}
	return scenes
}

func (s *Scraper) fetchScene(ctx context.Context, ref sceneRef, studioURL string, now time.Time) (*models.Scene, error) {
	pageURL := fmt.Sprintf("%s/watch/%s/%s.en.html", s.base, ref.id, ref.slug)
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	scene, err := parseScene(body, ref, studioURL, now)
	if err != nil {
		return nil, scraper.ParseError(pageURL, err)
	}
	scene.URL = fmt.Sprintf("%s/watch/%s/%s.en.html", siteBase, ref.id, ref.slug)
	return &scene, nil
}

// parseScene reads the detail page: an h1 title, schema.org microdata for the
// runtime, date and description, and the visible header block for the channel
// and cast.
func parseScene(body string, ref sceneRef, studioURL string, now time.Time) (models.Scene, error) {
	title := ""
	if m := titleRe.FindStringSubmatch(body); m != nil {
		title = cleanText(m[1])
	}
	if title == "" {
		return models.Scene{}, fmt.Errorf("no <h1> title on the page")
	}

	scene := models.Scene{
		ID:        ref.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     title,
		Studio:    studioName,
		ScrapedAt: now,
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := ogImageRe.FindStringSubmatch(body); m != nil {
		scene.Thumbnail = html.UnescapeString(m[1])
	}
	if m := durationRe.FindStringSubmatch(body); m != nil {
		scene.Duration = parseutil.ParseDurationISO("P" + strings.TrimPrefix(m[1], "P"))
	}
	if m := uploadRe.FindStringSubmatch(body); m != nil {
		if t, err := parseutil.TryParseDate(m[1], time.RFC3339); err == nil {
			scene.Date = t.UTC()
		}
	}

	header := headerBlock(body)
	if ch := chanLinkRe.FindStringSubmatch(header); ch != nil {
		scene.Studio = cleanText(ch[2])
	}
	seen := map[string]bool{}
	for _, m := range modelRe.FindAllStringSubmatch(header, -1) {
		name := cleanText(m[2])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		scene.Performers = append(scene.Performers, name)
	}
	scene.Tags = tags(body)
	return scene, nil
}

// headerBlock is the markup from the title to just past the cast line, which is
// where the channel and cast links live; the same shapes appear site-wide in
// nav and "related" rails.
func headerBlock(body string) string {
	stripped := scriptRe.ReplaceAllString(body, "")
	i := strings.Index(stripped, "<h1")
	if i < 0 {
		return ""
	}
	end := i + 1500
	if end > len(stripped) {
		end = len(stripped)
	}
	return stripped[i:end]
}

func tags(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range tagRe.FindAllStringSubmatch(body, -1) {
		name := strings.ReplaceAll(m[1], "-", " ")
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, " "))), " ")
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBodyN(resp.Body, 4<<20)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
