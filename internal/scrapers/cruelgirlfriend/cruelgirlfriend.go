package cruelgirlfriend

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "cruelgirlfriend"
	studioName = "Cruel Girlfriend"
	defaultURL = "https://cruelgf.com"
	// The catalogue is a flat list of clip ids, not paged, so the walk invents
	// its own pages: one chunk of detail fetches at a time, newest first, so
	// KnownIDs can still stop the run early.
	chunkSize      = 24
	defaultWorkers = 6
)

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(45 * time.Second), base: defaultURL}
}

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"cruelgf.com",
		"cruelgf.com/CGUpdates.php",
		"cruelgf.com/Girl.php?girlfriend={name}",
	}
}

var urlRe = regexp.MustCompile(`^https?://(?:www\.)?cruelgf\.com(?:/.*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return urlRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- discovery ----

type urlset struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

var clipNoRe = regexp.MustCompile(`(?i)clip_no=(\d+)`)

// clipIDs returns every clip id the sitemap names, newest first. Clip numbers
// rise with upload date on this site, so descending id order is date order —
// which is what lets the KnownIDs early-stop mean anything.
func (s *Scraper) clipIDs(ctx context.Context) ([]int, error) {
	body, err := s.get(ctx, s.base+"/sitemap.xml")
	if err != nil {
		return nil, err
	}
	var set urlset
	if err := xml.Unmarshal(body, &set); err != nil {
		return nil, scraper.ParseError(s.base+"/sitemap.xml", err)
	}
	seen := map[int]bool{}
	var ids []int
	for _, u := range set.URLs {
		m := clipNoRe.FindStringSubmatch(u.Loc)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		ids = append(ids, n)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	return ids, nil
}

// girlClipIDs reads one performer's page. It is not a sitemap, so the ids are
// whatever order the page lists them in; they are sorted the same way so the
// early stop behaves identically in both modes.
func (s *Scraper) girlClipIDs(ctx context.Context, pageURL string) ([]int, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var ids []int
	for _, m := range clipNoRe.FindAllStringSubmatch(string(body), -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		ids = append(ids, n)
	}
	if len(ids) == 0 {
		return nil, scraper.ParseError(pageURL, fmt.Errorf("no clips linked"))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	return ids, nil
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL: u,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- run ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	var (
		ids []int
		err error
	)
	if girl := girlfriendOf(studioURL); girl != "" {
		scraper.Debugf(1, "%s: scraping girlfriend %q", siteID, girl)
		ids, err = s.girlClipIDs(ctx, studioURL)
	} else {
		scraper.Debugf(1, "%s: scraping full catalogue from sitemap", siteID)
		ids, err = s.clipIDs(ctx)
	}
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("%s: %w", siteID, err)):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: %d clips to fetch", siteID, len(ids))

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		start := (page - 1) * chunkSize
		if start >= len(ids) {
			return scraper.PageResult{Done: true}, nil
		}
		end := min(start+chunkSize, len(ids))
		chunk := ids[start:end]

		scenes := s.fetchChunk(ctx, chunk, studioURL, workers, out)
		return scraper.PageResult{
			Scenes: scenes,
			Total:  len(ids),
			Done:   end >= len(ids),
			// A chunk whose every detail page failed is a bad patch of the
			// catalogue, not the end of it; the errors are already reported.
			Continue: true,
		}, nil
	})
}

var girlRe = regexp.MustCompile(`(?i)/Girl\.php$`)

func girlfriendOf(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil || !girlRe.MatchString(u.Path) {
		return ""
	}
	return u.Query().Get("girlfriend")
}

// fetchChunk resolves a chunk of clip ids concurrently but returns them in the
// order they were asked for: the early stop reads the first known id it sees,
// so a shuffled chunk would stop the walk at the wrong place.
func (s *Scraper) fetchChunk(ctx context.Context, ids []int, studioURL string, workers int, out chan<- scraper.SceneResult) []models.Scene {
	results := make([]*models.Scene, len(ids))
	errs := make([]error, len(ids))

	var wg sync.WaitGroup
	jobs := make(chan int)
	for range min(workers, len(ids)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				sc, err := s.fetchClip(ctx, ids[i], studioURL)
				if err != nil {
					errs[i] = err
					continue
				}
				results[i] = sc
			}
		}()
	}
	func() {
		defer close(jobs)
		for i := range ids {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()

	var scenes []models.Scene
	for i, sc := range results {
		if errs[i] != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("%s: clip %d: %w", siteID, ids[i], errs[i])):
			case <-ctx.Done():
				return scenes
			}
			continue
		}
		if sc != nil {
			scenes = append(scenes, *sc)
		}
	}
	return scenes
}

func (s *Scraper) clipURL(id int) string {
	return fmt.Sprintf("%s/Clip.php?clip_no=%d", s.base, id)
}

func (s *Scraper) fetchClip(ctx context.Context, id int, studioURL string) (*models.Scene, error) {
	u := s.clipURL(id)
	body, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}
	sc, err := parseClip(body, id, u, studioURL)
	if err != nil {
		return nil, err
	}
	return sc, nil
}

// ---- parsing ----

// Everything is read from the page rather than its schema.org block. The site
// emits the description into the JSON-LD with raw newlines still in it, which
// makes the block invalid JSON on any clip whose copy has a paragraph break —
// so the structured data is missing exactly where the longest descriptions are.
var (
	copyRe     = regexp.MustCompile(`(?is)<div class="media-video-copy">.*?<h3>(.*?)</h3>\s*<p>(.*?)</p>`)
	descRe     = regexp.MustCompile(`(?is)<div class="clip-description">(.*?)</div>`)
	addedRe    = regexp.MustCompile(`(?i)Added:\s*(\d{2}-\d{2}-\d{4})`)
	durationRe = regexp.MustCompile(`(?i)Duration:\s*(?:(\d+)h\s*)?(?:(\d+)m\s*)?(?:(\d+)s)?`)
	posterRe   = regexp.MustCompile(`(?is)<img[^>]*id="videoPosterOverlay"[^>]*src="([^"]+)"`)
	categoryRe = regexp.MustCompile(`(?i)Category\.php\?category=([^"'&]+)`)
	tagStripRe = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe       = regexp.MustCompile(`\s+`)
)

func parseClip(body []byte, id int, clipURL, studioURL string) (*models.Scene, error) {
	m := copyRe.FindSubmatch(body)
	if m == nil {
		return nil, scraper.ParseError(clipURL, fmt.Errorf("no clip copy block"))
	}
	title := cleanText(string(m[1]))
	if title == "" {
		return nil, scraper.ParseError(clipURL, fmt.Errorf("no title"))
	}

	sc := &models.Scene{
		ID:         strconv.Itoa(id),
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     studioName,
		Title:      title,
		URL:        clipURL,
		Performers: splitCast(cleanText(string(m[2]))),
		Duration:   parseDuration(body),
		ScrapedAt:  time.Now().UTC(),
	}
	if d := descRe.FindSubmatch(body); d != nil {
		sc.Description = cleanText(string(d[1]))
	}
	if a := addedRe.FindSubmatch(body); a != nil {
		if t, err := time.Parse("02-01-2006", string(a[1])); err == nil {
			sc.Date = t.UTC()
		}
	}
	if p := posterRe.FindSubmatch(body); p != nil {
		sc.Thumbnail = absURL(clipURL, html.UnescapeString(string(p[1])))
	}

	seen := map[string]bool{}
	for _, cm := range categoryRe.FindAllSubmatch(body, -1) {
		name, err := url.QueryUnescape(string(cm[1]))
		if err != nil {
			continue
		}
		name = cleanText(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		sc.Categories = append(sc.Categories, name)
	}
	return sc, nil
}

func parseDuration(body []byte) int {
	m := durationRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	atoi := func(b []byte) int {
		n, _ := strconv.Atoi(string(b))
		return n
	}
	return atoi(m[1])*3600 + atoi(m[2])*60 + atoi(m[3])
}

func absURL(pageURL, ref string) string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return ref
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return base.ResolveReference(u).String()
}

// splitCast reads the cast line under a clip title. Two performers are joined
// with a hyphen ("Jessie - Cashleigh"), which is why the separator is the
// spaced form: hyphenated single names exist.
func splitCast(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, " - ") {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

// cleanText unescapes before stripping tags: the site double-encodes its copy,
// so a <br> arrives as &lt;br&gt; and survives a strip-then-unescape pass.
func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}
