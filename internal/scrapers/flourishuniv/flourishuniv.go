package flourishuniv

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID      = "flourishuniv"
	studioName  = "Flourish University"
	episodesURL = "https://www.flourishuniv.com/episodes/"
)

func init() { scraper.Register(New()) }

type Scraper struct {
	client     *http.Client
	base       string
	detailBase string
}

func New() *Scraper {
	return &Scraper{
		client:     httpx.NewClient(30 * time.Second),
		base:       "https://www.flourishuniv.com",
		detailBase: "https://tour.theflourishxxx.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"flourishuniv.com/episodes/",
	}
}

func (s *Scraper) MatchesURL(u string) bool {
	return scraper.HostMatches(u, "flourishuniv.com")
}

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

var (
	cardRe        = regexp.MustCompile(`<div class="episode-card">`)
	titleRe       = regexp.MustCompile(`<h3>([^<]+)</h3>`)
	thumbRe       = regexp.MustCompile(`<img src="([^"]+)"[^>]*alt="([^"]*)"`)
	epNumRe       = regexp.MustCompile(`<div class="ep-num">([^<]+)</div>`)
	descRe        = regexp.MustCompile(`(?s)<div class="ep-desc">(.*?)</div>`)
	watchURLRe    = regexp.MustCompile(`<a href="([^"]+/trailers/[^"]+\.html)"`)
	starringRe    = regexp.MustCompile(`(?i)Starring:\s*(.+)`)
	trailerSlugRe = regexp.MustCompile(`/trailers/([^/]+)\.html`)

	detailDateRe    = regexp.MustCompile(`Added:\s*(\w+ \d{1,2}, \d{4})`)
	detailRuntimeRe = regexp.MustCompile(`Runtime:\s*(?:\d+[^,]*,\s*)?(\d+:\d{2}(?::\d{2})?)`)
	detailTagsRe    = regexp.MustCompile(`(?s)<ul class="tags">(.*?)</ul>`)
	detailTagRe     = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
	detailDescRe    = regexp.MustCompile(`(?s)<div class="description">\s*<h3>[^<]*</h3>\s*<p>(.*?)</p>`)

	brTagRe    = regexp.MustCompile(`<br\s*/?\s*>`)
	stripTagRe = regexp.MustCompile(`<[^>]+>`)
)

type episode struct {
	slug       string
	title      string
	thumb      string
	desc       string
	performers []string
	epNum      string
	watchURL   string
}

func parseEpisodes(body []byte) []episode {
	page := string(body)
	starts := cardRe.FindAllStringIndex(page, -1)
	eps := make([]episode, 0, len(starts))

	for i, loc := range starts {
		start := loc[0]
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[start:end]

		var ep episode

		if m := watchURLRe.FindStringSubmatch(block); m != nil {
			ep.watchURL = m[1]
			if sm := trailerSlugRe.FindStringSubmatch(ep.watchURL); sm != nil {
				ep.slug = sm[1]
			}
		}
		if ep.slug == "" {
			continue
		}

		if m := titleRe.FindStringSubmatch(block); m != nil {
			ep.title = strings.TrimSpace(html.UnescapeString(m[1]))
		}

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			ep.thumb = m[1]
		}

		if m := epNumRe.FindStringSubmatch(block); m != nil {
			ep.epNum = strings.TrimSpace(m[1])
		}

		if m := descRe.FindStringSubmatch(block); m != nil {
			raw := m[1]
			raw = stripTagRe.ReplaceAllString(raw, "\n")
			raw = html.UnescapeString(raw)
			lines := strings.Split(raw, "\n")
			var descParts []string
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if sm := starringRe.FindStringSubmatch(line); sm != nil {
					ep.performers = parseCast(sm[1])
					continue
				}
				descParts = append(descParts, line)
			}
			ep.desc = strings.Join(descParts, "\n")
		}

		eps = append(eps, ep)
	}
	return eps
}

func parseCast(s string) []string {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ",")
	var names []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, "with ")
		p = strings.TrimPrefix(p, "and ")
		p = strings.TrimSpace(p)
		if p != "" {
			names = append(names, p)
		}
	}
	return names
}

type detailData struct {
	date        time.Time
	duration    int
	tags        []string
	description string
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := detailDateRe.FindSubmatch(body); m != nil {
		if t, err := time.Parse("January 2, 2006", string(m[1])); err == nil {
			d.date = t.UTC()
		}
	}

	if m := detailRuntimeRe.FindSubmatch(body); m != nil {
		d.duration = parseutil.ParseDurationColon(string(m[1]))
	}

	if m := detailTagsRe.FindSubmatch(body); m != nil {
		for _, tm := range detailTagRe.FindAllSubmatch(m[1], -1) {
			tag := strings.TrimSpace(string(tm[1]))
			if tag != "" {
				d.tags = append(d.tags, tag)
			}
		}
	}

	if m := detailDescRe.FindSubmatch(body); m != nil {
		raw := strings.TrimSpace(string(m[1]))
		raw = brTagRe.ReplaceAllString(raw, "\n")
		raw = stripTagRe.ReplaceAllString(raw, "")
		d.description = strings.TrimSpace(html.UnescapeString(raw))
	}

	return d
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()

	listURL := s.base + "/episodes/"
	body, err := s.fetchPage(ctx, listURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	eps := parseEpisodes(body)
	if len(eps) == 0 {
		return
	}

	select {
	case out <- scraper.Progress(len(eps)):
	case <-ctx.Done():
		return
	}

	for _, ep := range eps {
		if opts.KnownIDs[ep.slug] {
			scraper.Debugf(1, "flourishuniv: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}

		if !scraper.Pace(ctx, opts.Delay) {
			return
		}

		scene, err := s.buildScene(ctx, ep, now)
		if err != nil {
			select {
			case out <- scraper.Error(err):
			case <-ctx.Done():
				return
			}
		}
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}
}

// buildScene builds a scene from its listing entry and enriches it from the
// watch page. The listing already names the episode, so a dead watch page
// costs its date, runtime and tags rather than the scene — but the failure is
// returned, and the caller reports it so an authoritative --full Save cannot
// treat a half-read catalogue as complete.
func (s *Scraper) buildScene(ctx context.Context, ep episode, now time.Time) (models.Scene, error) {
	scene := models.Scene{
		ID:          ep.slug,
		SiteID:      siteID,
		StudioURL:   episodesURL,
		Title:       ep.title,
		URL:         ep.watchURL,
		Thumbnail:   ep.thumb,
		Performers:  ep.performers,
		Description: ep.desc,
		Studio:      studioName,
		Series:      "Flourish University",
		ScrapedAt:   now,
	}

	body, err := s.fetchPage(ctx, ep.watchURL)
	if err != nil {
		return scene, fmt.Errorf("detail %s: %w", ep.watchURL, err)
	}
	detail := parseDetailPage(body)
	scene.Date = detail.date
	scene.Duration = detail.duration
	scene.Tags = detail.tags
	if detail.description != "" && scene.Description == "" {
		scene.Description = detail.description
	}

	return scene, nil
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
