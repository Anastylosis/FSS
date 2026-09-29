package titanmen

import (
	"context"
	"fmt"
	"html"
	"net/http"
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

type Scraper struct {
	client       *http.Client
	baseOverride string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "titanmen" }

func (s *Scraper) Patterns() []string {
	return []string{
		"titanmen.com",
		"titanmen.com/category.php?id={N}",
		"titanmen.com/sets.php?id={N}",
		"titanmen.com/dvds.php?id={N}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?titanmen\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

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
	return "https://www.titanmen.com"
}

type listEntry struct {
	sceneID    string
	dvdID      string
	title      string
	performers []string
	date       string
	duration   string
	thumbnail  string
}

type urlMode int

const (
	modeScenes urlMode = iota
	modeModel
	modeDVD
	modeCategory
)

var (
	modelPathRe    = regexp.MustCompile(`sets\.php\?id=(\d+)`)
	dvdPathRe      = regexp.MustCompile(`dvds\.php\?id=(\d+)(?:&|$)`)
	categoryPathRe = regexp.MustCompile(`category\.php\?id=(\d+)`)
)

func detectMode(rawURL string) urlMode {
	if modelPathRe.MatchString(rawURL) {
		return modeModel
	}
	if dvdPathRe.MatchString(rawURL) && !strings.Contains(rawURL, "sceneid=") {
		return modeDVD
	}
	if categoryPathRe.MatchString(rawURL) {
		return modeCategory
	}
	return modeScenes
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	mode := detectMode(studioURL)

	switch mode {
	case modeModel, modeDVD:
		s.runSinglePage(ctx, studioURL, opts, out)
	default:
		s.runPaginated(ctx, studioURL, opts, out, mode)
	}
}

func (s *Scraper) runSinglePage(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetchHTML(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	entries := parseListingEntries(body)
	if len(entries) == 0 {
		return
	}

	select {
	case out <- scraper.Progress(len(entries)):
	case <-ctx.Done():
		return
	}

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
	defer func() {
		close(work)
		wg.Wait()
	}()

	// A model page and a DVD page are both a single listing that is already in
	// hand, and neither is date-ordered — a DVD lists its scenes by disc
	// position, so a bonus scene appended to a disc sits behind scenes that
	// shipped with it. Stopping at the first stored scene hid those forever,
	// so known scenes are skipped and the rest of the page is still queued.
	skipped := 0
	for _, e := range entries {
		if opts.KnownIDs[e.sceneID] {
			skipped++
			continue
		}
		select {
		case work <- e:
		case <-ctx.Done():
			return
		}
	}
	if skipped > 0 {
		scraper.Debugf(1, "titanmen: skipped %d already-stored scene(s)", skipped)
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
}

func (s *Scraper) runPaginated(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, mode urlMode) {
	baseURL := s.buildBaseURL(studioURL, mode)

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
		scraper.Debugf(1, "titanmen: fetching page %d", page)

		pageURL := fmt.Sprintf("%s&page=%d", baseURL, page)
		body, err := s.fetchHTML(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		entries := parseListingEntries(body)
		if len(entries) == 0 {
			break
		}

		totalPages := parseTotalPages(body)
		if !sentTotal && totalPages > 0 {
			sentTotal = true
			select {
			case out <- scraper.Progress(totalPages * len(entries)):
			case <-ctx.Done():
			}
		}

		cancelled := false
		hitKnown := false
		for _, e := range entries {
			if opts.KnownIDs[e.sceneID] {
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
				scraper.Debugf(1, "titanmen: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
			}
			break
		}

		if totalPages > 0 && page >= totalPages {
			break
		}
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) buildBaseURL(rawURL string, mode urlMode) string {
	b := s.base()
	if mode == modeCategory {
		if m := categoryPathRe.FindStringSubmatch(rawURL); m != nil {
			return fmt.Sprintf("%s/category.php?id=%s&s=d", b, m[1])
		}
	}
	return fmt.Sprintf("%s/category.php?id=5&s=d", b)
}

var (
	sceneGridItemRe = regexp.MustCompile(`(?s)<div id="scene-grid-item-(\d+)"[^>]*>.*?</div><!-- end scene-grid-item -->`)
	sceneLinkRe     = regexp.MustCompile(`href="dvds\.php\?id=(\d+)&(?:amp;)?sceneid=(\d+)"`)
	sceneTitleRe    = regexp.MustCompile(`class="scene-link-\d+ scene-link">([^<]+)</a>`)
	overlayStarsRe  = regexp.MustCompile(`<div class="overlay-stars">([^<]+)</div>`)
	overlayDateRe   = regexp.MustCompile(`<strong>Released:</strong>\s*([A-Z][a-z]{2}\s+\d{1,2},\s*\d{4})`)
	overlayLenRe    = regexp.MustCompile(`<strong>Length:</strong>\s*(\d+:\d+)`)
	thumbSrcRe      = regexp.MustCompile(`src="([^"]+contentthumbs[^"]+)"`)
	totalPagesRe    = regexp.MustCompile(`(\d+)\s+of\s+(\d+)`)
)

func parseListingEntries(body []byte) []listEntry {
	items := sceneGridItemRe.FindAll(body, -1)
	entries := make([]listEntry, 0, len(items))
	seen := make(map[string]bool)

	for _, item := range items {
		m := sceneLinkRe.FindSubmatch(item)
		if m == nil {
			continue
		}
		dvdID := string(m[1])
		sceneID := string(m[2])
		if seen[sceneID] {
			continue
		}
		seen[sceneID] = true

		e := listEntry{
			sceneID: sceneID,
			dvdID:   dvdID,
		}

		if tm := sceneTitleRe.FindSubmatch(item); tm != nil {
			e.title = html.UnescapeString(strings.TrimSpace(string(tm[1])))
		}

		if sm := overlayStarsRe.FindSubmatch(item); sm != nil {
			for _, p := range strings.Split(string(sm[1]), ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					e.performers = append(e.performers, html.UnescapeString(p))
				}
			}
		}

		if dm := overlayDateRe.FindSubmatch(item); dm != nil {
			e.date = string(dm[1])
		}

		if lm := overlayLenRe.FindSubmatch(item); lm != nil {
			e.duration = string(lm[1])
		}

		if ts := thumbSrcRe.FindSubmatch(item); ts != nil {
			e.thumbnail = html.UnescapeString(string(ts[1]))
		}

		entries = append(entries, e)
	}

	return entries
}

func parseTotalPages(body []byte) int {
	m := totalPagesRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[2]))
	return n
}

var (
	detailTitleRe     = regexp.MustCompile(`<h1 class="scene-header-title">([^<]+)</h1>`)
	featuringRe       = regexp.MustCompile(`(?s)<strong>Featuring:\s*</strong>(.*?)</p>`)
	performerLinkRe   = regexp.MustCompile(`<a href="sets\.php\?id=\d+">([^<]+)</a>`)
	detailReleasedRe  = regexp.MustCompile(`<p><strong>Released:\s*</strong>\s*([A-Z][a-z]{2}\s+\d{1,2},\s*\d{4})</p>`)
	detailLengthRe    = regexp.MustCompile(`<p><strong>Length:</strong>\s*(\d+):(\d+)</p>`)
	descriptionRe     = regexp.MustCompile(`(?s)<h2>Description</h2><p>(.*?)</p>`)
	categoriesRe      = regexp.MustCompile(`(?s)<p><strong>Categories:\s*</strong>(.*?)</p>`)
	categoryLinkRe    = regexp.MustCompile(`<a href="category\.php\?[^"]*">([^<]+)</a>`)
	movieTitleRe      = regexp.MustCompile(`<p><strong>Movie Title:\s*</strong><a href="[^"]*">([^<]+)</a></p>`)
	directorRe        = regexp.MustCompile(`<p><strong>Director:\s*</strong><a href="[^"]*">([^<]+)</a></p>`)
	detailThumbLinkRe = regexp.MustCompile(`<a href="dvds\.php\?id=\d+&(?:amp;)?sceneid=(\d+)"[^>]*><img src="([^"]+)"`)
)

func (s *Scraper) fetchDetail(ctx context.Context, entry listEntry) (models.Scene, error) {
	u := fmt.Sprintf("%s/dvds.php?id=%s&sceneid=%s", s.base(), entry.dvdID, entry.sceneID)
	body, err := s.fetchHTML(ctx, u)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", entry.sceneID, err)
	}

	return parseDetail(body, entry, s.base()), nil
}

func parseDetail(body []byte, entry listEntry, _ string) models.Scene {
	now := time.Now().UTC()
	scene := models.Scene{
		ID:         entry.sceneID,
		SiteID:     "titanmen",
		StudioURL:  "https://www.titanmen.com",
		Title:      entry.title,
		URL:        fmt.Sprintf("https://www.titanmen.com/dvds.php?id=%s&sceneid=%s", entry.dvdID, entry.sceneID),
		Thumbnail:  entry.thumbnail,
		Performers: entry.performers,
		ScrapedAt:  now,
	}

	if m := detailTitleRe.FindSubmatch(body); m != nil {
		scene.Title = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := featuringRe.FindSubmatch(body); m != nil {
		var perfs []string
		for _, pm := range performerLinkRe.FindAllSubmatch(m[1], -1) {
			perfs = append(perfs, html.UnescapeString(strings.TrimSpace(string(pm[1]))))
		}
		if len(perfs) > 0 {
			scene.Performers = perfs
		}
	}

	if m := detailReleasedRe.FindSubmatch(body); m != nil {
		if t, err := time.Parse("Jan 2, 2006", strings.TrimSpace(string(m[1]))); err == nil {
			scene.Date = t.UTC()
		}
	} else if entry.date != "" {
		if t, err := time.Parse("Jan 2, 2006", strings.TrimSpace(entry.date)); err == nil {
			scene.Date = t.UTC()
		}
	}

	if m := detailLengthRe.FindSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(string(m[1]))
		secs, _ := strconv.Atoi(string(m[2]))
		scene.Duration = mins*60 + secs
	} else if entry.duration != "" {
		scene.Duration = parseutil.ParseDurationColon(entry.duration)
	}

	if m := descriptionRe.FindSubmatch(body); m != nil {
		scene.Description = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}

	if m := categoriesRe.FindSubmatch(body); m != nil {
		for _, cm := range categoryLinkRe.FindAllSubmatch(m[1], -1) {
			tag := strings.TrimSpace(html.UnescapeString(string(cm[1])))
			if tag != "Scenes" {
				scene.Tags = append(scene.Tags, tag)
			}
		}
	}

	if m := movieTitleRe.FindSubmatch(body); m != nil {
		scene.Studio = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := directorRe.FindSubmatch(body); m != nil {
		scene.Director = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := detailThumbLinkRe.FindAllSubmatch(body, -1); m != nil {
		for _, match := range m {
			if string(match[1]) == entry.sceneID {
				scene.Thumbnail = html.UnescapeString(string(match[2]))
				break
			}
		}
	}

	return scene
}

func (s *Scraper) fetchHTML(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: rawURL,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Cookie"] = "age_gate_accepted=1"
			return h
		}(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
