// Package laceystarr scrapes laceystarr.com, a hand-rolled PHP tour with no
// per-scene pages. See docs/scrapers.md.
package laceystarr

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

const (
	siteID      = "laceystarr"
	studioName  = "Lacey Starr"
	defaultBase = "https://www.laceystarr.com"
	tourPath    = "/granny/"
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?laceystarr\.com(?:/|$)`)
	modelRe = regexp.MustCompile(`/granny/model-([^/?#]+)\.php`)
	dvdRe   = regexp.MustCompile(`/granny/dvd-(\d+)\.php`)

	commentRe  = regexp.MustCompile(`(?s)<!--.*?-->`)
	cardRe     = regexp.MustCompile(`<div class="otherScene">`)
	dvdCardRe  = regexp.MustCompile(`<div class="dvdScene clear">`)
	thumbRe    = regexp.MustCompile(`src="([^"]*/contentthumbs/(\d+)\.jpg)"`)
	h4TitleRe  = regexp.MustCompile(`(?s)<h4>(.*?)</h4>`)
	h3TitleRe  = regexp.MustCompile(`(?s)<h3>(.*?)</h3>`)
	minutesRe  = regexp.MustCompile(`(\d+)\s*min\b`)
	dateRe     = regexp.MustCompile(`fa-calendar"></i>\s*([^<]+)`)
	descRe     = regexp.MustCompile(`data-desc="([^"]*)"`)
	dvdDescRe  = regexp.MustCompile(`(?s)class="dvdDescription">(.*?)</p>`)
	starringRe = regexp.MustCompile(`(?s)Starring:(.*?)</p>`)
	performRe  = regexp.MustCompile(`<a href="model-([^"]+)\.php">([^<]+)</a>`)
	nextRe     = regexp.MustCompile(`class="next page-numbers"`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

// Scraper implements scraper.StudioScraper for laceystarr.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Lacey Starr scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: defaultBase}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"laceystarr.com",
		"laceystarr.com/granny/",
		"laceystarr.com/granny/updates.php",
		"laceystarr.com/granny/model-{slug}.php",
		"laceystarr.com/granny/dvd-{id}.php",
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

	if m := dvdRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping DVD %s", siteID, m[1])
		s.runDVD(ctx, studioURL, m[1], out)
		return
	}

	model := ""
	if m := modelRe.FindStringSubmatch(studioURL); m != nil {
		// A model page shows only her newest 20 scenes, so the catalogue is
		// walked and filtered on the cast links instead.
		model = m[1]
		scraper.Debugf(1, "%s: scraping model %s via the updates listing", siteID, model)
	}

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%supdates.php?p=%d", s.base, tourPath, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parseListing(body, s.base, studioURL, now)
		more := nextRe.MatchString(body)
		if len(scenes) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first updates page"))
			}
			return scraper.PageResult{Done: true}, nil
		}
		if model != "" {
			scenes = filterModel(scenes, model)
			return scraper.PageResult{Scenes: scenes, Done: !more, Continue: true}, nil
		}
		for i := range scenes {
			scenes[i].Performers = performerNames(scenes[i].Performers)
		}
		return scraper.PageResult{Scenes: scenes, Done: !more}, nil
	})
}

func (s *Scraper) runDVD(ctx context.Context, studioURL, id string, out chan<- scraper.SceneResult) {
	pageURL := fmt.Sprintf("%s%sdvd-%s.php", s.base, tourPath, id)
	body, err := s.fetch(ctx, pageURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	scenes := parseDVD(body, s.base, studioURL, time.Now().UTC())
	if len(scenes) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(pageURL, fmt.Errorf("no scenes on the DVD page"))))
		return
	}
	if !send(ctx, out, scraper.Progress(len(scenes))) {
		return
	}
	for _, sc := range scenes {
		if !send(ctx, out, scraper.Scene(sc)) {
			return
		}
	}
}

// parseListing reads the update cards of one updates.php page. Performers are
// returned as "slug\x00Name" pairs so a model filter can match on the slug;
// performerNames strips them back to names.
func parseListing(body, base, studioURL string, now time.Time) []models.Scene {
	body = commentRe.ReplaceAllString(body, "")
	if i := strings.Index(body, `class="pagination"`); i >= 0 {
		body = body[:i]
	}
	blocks := splitOn(body, cardRe)
	scenes := make([]models.Scene, 0, len(blocks))
	for _, block := range blocks {
		th := thumbRe.FindStringSubmatch(block)
		t := h4TitleRe.FindStringSubmatch(block)
		if th == nil || t == nil {
			continue
		}
		title := cleanText(t[1])
		if title == "" {
			continue
		}
		sc := newScene(base, studioURL, th, title, now)
		sc.URL = base + tourPath + "updates.php#" + th[2]
		sc.Duration = parseMinutes(block)
		if d := dateRe.FindStringSubmatch(block); d != nil {
			if parsed, err := parseutil.TryParseDate(strings.TrimSpace(d[1]), "January 2, 2006"); err == nil {
				sc.Date = parsed
			}
		}
		if d := descRe.FindStringSubmatch(block); d != nil {
			sc.Description = cleanText(d[1])
		}
		if st := starringRe.FindStringSubmatch(block); st != nil {
			for _, p := range performRe.FindAllStringSubmatch(st[1], -1) {
				if name := cleanText(p[2]); name != "" {
					sc.Performers = append(sc.Performers, p[1]+"\x00"+name)
				}
			}
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

// parseDVD reads a DVD page's scene blocks: title, runtime and description,
// but no date or cast.
func parseDVD(body, base, studioURL string, now time.Time) []models.Scene {
	body = commentRe.ReplaceAllString(body, "")
	blocks := splitOn(body, dvdCardRe)
	scenes := make([]models.Scene, 0, len(blocks))
	for _, block := range blocks {
		th := thumbRe.FindStringSubmatch(block)
		t := h3TitleRe.FindStringSubmatch(block)
		if th == nil || t == nil {
			continue
		}
		title := cleanText(t[1])
		if title == "" {
			continue
		}
		sc := newScene(base, studioURL, th, title, now)
		sc.URL = studioURL + "#" + th[2]
		sc.Duration = parseMinutes(block)
		if d := dvdDescRe.FindStringSubmatch(block); d != nil {
			sc.Description = strings.TrimSpace(strings.TrimPrefix(cleanText(d[1]), "Description:"))
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

// newScene keys a scene on its content-thumbnail number, the only per-scene
// value the tour publishes for every update (the trailer id is present only
// on scenes that have a trailer). Every link on a card goes to the join form.
func newScene(base, studioURL string, th []string, title string, now time.Time) models.Scene {
	thumb := th[1]
	if strings.HasPrefix(thumb, "//") {
		thumb = "https:" + thumb
	} else if strings.HasPrefix(thumb, "/") {
		thumb = base + thumb
	}
	return models.Scene{
		ID:        th[2],
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     title,
		Thumbnail: thumb,
		ScrapedAt: now,
	}
}

func splitOn(body string, re *regexp.Regexp) []string {
	idx := re.FindAllStringIndex(body, -1)
	blocks := make([]string, 0, len(idx))
	for i, m := range idx {
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		blocks = append(blocks, body[m[1]:end])
	}
	return blocks
}

func filterModel(scenes []models.Scene, slug string) []models.Scene {
	var kept []models.Scene
	for _, sc := range scenes {
		match := false
		for _, p := range sc.Performers {
			if strings.EqualFold(strings.SplitN(p, "\x00", 2)[0], slug) {
				match = true
				break
			}
		}
		if match {
			sc.Performers = performerNames(sc.Performers)
			kept = append(kept, sc)
		}
	}
	return kept
}

func performerNames(pairs []string) []string {
	names := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts := strings.SplitN(p, "\x00", 2)
		names = append(names, parts[len(parts)-1])
	}
	return names
}

func parseMinutes(block string) int {
	m := minutesRe.FindStringSubmatch(block)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n * 60
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
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

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}
