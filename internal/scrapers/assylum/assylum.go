// Package assylum scrapes assylum.com and its sister site slavemouth.com, which
// run the same Derange Dollars CMS and serve `/session//{id}` identically.
//
// The site publishes no complete listing and no sitemap. `?sessions` shows
// twenty at a time and its `so=` parameter re-sorts rather than paginating —
// sweeping `so=0…59` reaches 109 distinct sessions and plateaus there, against
// a catalogue several times that. What every session does have is a numeric id
// (`data-lid`), and `/session//{id}` serves it directly.
//
// So the walk is an id sweep: the highest id in reach is learned from the home
// page and a handful of `?sessions&so=` views, and every id from 1 to that
// (plus a margin for scenes published since) is probed. Absent ids answer 404
// or a near-empty 200 and cost one request each; the sweep is bounded and
// finishes in a few minutes at the default delay.
package assylum

import (
	"context"
	"errors"
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
	"github.com/Anastylosis/FSS/scraper"
)

type site struct {
	id     string
	domain string
	name   string
	match  *regexp.Regexp
}

var sites = []site{
	{"assylum", "assylum.com", "Assylum", regexp.MustCompile(`^https?://(?:www\.)?assylum\.com(?:/|$)`)},
	{"slavemouth", "slavemouth.com", "Slavemouth", regexp.MustCompile(`^https?://(?:www\.)?slavemouth\.com(?:/|$)`)},
}

const (
	// probeMargin is how far past the highest id in reach the sweep continues,
	// so a scene published between the index being written and the sweep
	// running is still found.
	probeMargin = 25
	// maxProbeID bounds the sweep even if the discovered maximum is nonsense.
	// Ids reach the high 700s today.
	maxProbeID = 5000
	// sortViews is how many `?sessions&so=` views are consulted to learn the
	// upper bound. They overlap heavily; a handful is enough to find the top.
	sortViews = 6
)

type Scraper struct {
	site         site
	Client       *http.Client
	baseOverride string
}

func New() *Scraper { return newFor(sites[0]) }

func newFor(st site) *Scraper {
	return &Scraper{site: st, Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	for _, st := range sites {
		scraper.Register(newFor(st))
	}
}

var (
	// Session links carry a free-text path before the id, which the site does
	// not require: `/session//{id}` serves the same page.
	sessionLinkRe = regexp.MustCompile(`\./session/[^"]*?(\d+)"`)
	sessionURLRe  = regexp.MustCompile(`/session/[^?#]*?(\d+)/?$`)
)

func (s *Scraper) ID() string { return s.site.id }

func (s *Scraper) Patterns() []string {
	return []string{
		s.site.domain,
		s.site.domain + "/?sessions",
		s.site.domain + "/session//{id}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.site.match.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// base resolves the origin to fetch from. The operator's own host wins so a
// non-www spelling stays addressable; baseOverride is the test server.
func (s *Scraper) base(studioURL string) string {
	if s.baseOverride != "" {
		return strings.TrimSuffix(s.baseOverride, "/")
	}
	if u, err := url.Parse(studioURL); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return "https://www." + s.site.domain
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := s.base(studioURL)

	// A single session URL is a legitimate thing to point at.
	if m := sessionURLRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping one session %s", s.site.id, m[1])
		if !send(ctx, out, scraper.Progress(1)) {
			return
		}
		if sc, ok := s.fetchSession(ctx, studioURL, base, m[1], out); ok {
			send(ctx, out, scraper.Scene(sc))
		}
		return
	}

	highest, ok := s.highestID(ctx, base, opts, out)
	if !ok {
		return
	}
	limit := highest + probeMargin
	if limit > maxProbeID {
		limit = maxProbeID
	}
	scraper.Debugf(1, "%s: sweeping ids 1..%d with %d workers", s.site.id, limit, opts.Workers)

	// The count is not knowable before the sweep, so no Progress total is sent
	// up front — the cmd layer shows a running count instead.
	work := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range work {
				if !sleep(ctx, opts.Delay) {
					return
				}
				sc, found := s.fetchSession(ctx, studioURL, base, strconv.Itoa(id), out)
				if !found {
					continue
				}
				if !send(ctx, out, scraper.Scene(sc)) {
					return
				}
			}
		}()
	}

	for id := 1; id <= limit; id++ {
		if opts.KnownIDs[strconv.Itoa(id)] {
			// The sweep is by id, not by date, so a stored id says nothing
			// about the ids after it — it is skipped, never a stop.
			continue
		}
		select {
		case work <- id:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return
		}
	}
	close(work)
	wg.Wait()
}

// highestID learns the top of the id range from the home page and a few
// `?sessions&so=` views. It reports false when nothing could be read, which is
// a template change rather than an empty site.
func (s *Scraper) highestID(ctx context.Context, base string, opts scraper.ListOpts, out chan<- scraper.SceneResult) (int, bool) {
	pages := []string{base + "/?home"}
	for i := 0; i < sortViews; i++ {
		pages = append(pages, fmt.Sprintf("%s/?sessions&so=%d", base, i))
	}

	highest := 0
	reachable := 0
	for i, p := range pages {
		if ctx.Err() != nil {
			return 0, false
		}
		if i > 0 && !sleep(ctx, opts.Delay) {
			return 0, false
		}
		body, err := s.fetch(ctx, p)
		if err != nil {
			// One index view is not the catalogue; the others still bound it.
			send(ctx, out, scraper.Error(fmt.Errorf("index %s: %w", p, err)))
			continue
		}
		for _, m := range sessionLinkRe.FindAllStringSubmatch(string(body), -1) {
			reachable++
			if n, err := strconv.Atoi(m[1]); err == nil && n > highest {
				highest = n
			}
		}
	}

	if highest == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(base+"/?home",
			fmt.Errorf("no session links on the index pages — cannot bound the id sweep"))))
		return 0, false
	}
	scraper.Debugf(1, "%s: %d session links across the index views, highest id %d", s.site.id, reachable, highest)
	return highest, true
}

// fetchSession reads one session page. `found` is false for an id the site does
// not serve, which is the ordinary outcome for most of the sweep and is not
// reported as an error.
func (s *Scraper) fetchSession(ctx context.Context, studioURL, base, id string, out chan<- scraper.SceneResult) (models.Scene, bool) {
	sceneURL := base + "/session//" + id

	body, err := s.fetch(ctx, sceneURL)
	if err != nil {
		if isNotFound(err) {
			return models.Scene{}, false
		}
		send(ctx, out, scraper.Error(fmt.Errorf("session %s: %w", id, err)))
		return models.Scene{}, false
	}

	d := parseSession(string(body))
	if d.id == "" || d.title == "" {
		// A gap in the id range serves a near-empty page rather than a 404.
		return models.Scene{}, false
	}

	scene := models.Scene{
		ID:          d.id,
		SiteID:      s.site.id,
		StudioURL:   studioURL,
		Title:       d.title,
		URL:         sceneURL,
		Studio:      s.site.name,
		Description: d.description,
		Performers:  d.performers,
		Date:        d.date,
		Thumbnail:   absolutize(d.thumb, base),
		ScrapedAt:   time.Now().UTC(),
	}
	return scene, true
}

func isNotFound(err error) bool {
	var se *httpx.StatusError
	return errors.As(err, &se) && se.StatusCode == http.StatusNotFound
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		Method:  http.MethodGet,
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- session page ----

type sessionData struct {
	id          string
	title       string
	description string
	performers  []string
	date        time.Time
	thumb       string
}

var (
	scriptRe = regexp.MustCompile(`(?s)<script.*?</script>`)
	lidRe    = regexp.MustCompile(`data-lid="(\d+)"`)
	titleRe  = regexp.MustCompile(`(?s)<h3 class="mas_title">(.*?)</h3>`)
	// "Nuria Millan, July 24, 2026" — cast then date, in one span.
	infoRe   = regexp.MustCompile(`(?s)<span class="lc_info mas_description">(.*?)</span>`)
	longDesc = regexp.MustCompile(`(?s)<p class="mas_longdescription">(.*?)</p>`)
	faceImg  = regexp.MustCompile(`src="(faceimages/[^"]+)"`)
	dateRe   = regexp.MustCompile(`([A-Z][a-z]+\s+\d{1,2},\s*\d{4})`)
)

func parseSession(body string) sessionData {
	body = scriptRe.ReplaceAllString(body, "")
	d := sessionData{
		id:          firstSubmatch(lidRe, body),
		title:       cleanText(firstSubmatch(titleRe, body)),
		description: cleanText(firstSubmatch(longDesc, body)),
		thumb:       firstSubmatch(faceImg, body),
	}

	info := cleanText(firstSubmatch(infoRe, body))
	if m := dateRe.FindStringSubmatch(info); m != nil {
		if t, err := time.Parse("January 2, 2006", strings.Join(strings.Fields(m[1]), " ")); err == nil {
			d.date = t.UTC()
		}
		// Everything before the date is the cast, comma-separated.
		info = strings.TrimSpace(info[:strings.Index(info, m[1])])
	}
	for _, part := range strings.Split(strings.Trim(info, " ,"), ",") {
		if n := cleanText(stripRoleLabel(part)); n != "" {
			d.performers = appendUnique(d.performers, n)
		}
	}
	return d
}

// stripRoleLabel drops the in-fiction role the site prefixes many credits with
// — `Patient: Riley Reynolds`, `Nurse: …`, `Student: …`. 102 of 297 credits
// carry one, and keeping it files the same person under two names, since the
// rest of her scenes credit her plainly. Performer names carry no colon of
// their own, so the first one is always the label's.
func stripRoleLabel(s string) string {
	if _, after, ok := strings.Cut(s, ":"); ok {
		return after
	}
	return s
}

// ---- helpers ----

var tagStripRe = regexp.MustCompile(`<[^>]*>`)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.Join(strings.Fields(s), " ")
}

func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func absolutize(ref, base string) string {
	switch {
	case ref == "":
		return ""
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return ref
	case strings.HasPrefix(ref, "/"):
		return base + ref
	default:
		return base + "/" + ref
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}
