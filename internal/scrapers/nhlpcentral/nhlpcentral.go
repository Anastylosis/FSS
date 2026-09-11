// Package nhlpcentral scrapes NHLP Central (nhlpcentral.com), the Nylons Cash
// hub that absorbed Vintage Flash, Pantyhosed4U, PantyFlash Girls and The Joy
// of Feet (all of which now redirect here).
//
// The tour has no paginated listing of the catalogue. Every set is served at
// /update.php?id=N, and ids are neither dense nor in date order (an old id can
// be re-released with a new date), so the full catalogue is enumerated by
// walking ids; an unused id answers HTTP 200 with a near-empty body. The
// homepage lists the latest releases newest-first, which is what incremental
// runs check for known ids before falling back to the walk. Model pages
// (/bio.php?name=X) page through that model's sets four at a time.
package nhlpcentral

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID         = "nhlpcentral"
	studioName     = "NHLP Central"
	defaultWorkers = 4
	// batchSize is how many ids are probed per round of the walk.
	batchSize = 24
	// maxGap is how many consecutive unused ids end the walk. The largest
	// interior run observed is 261 (ids 1209–1469).
	maxGap = 300
	// maxID is a backstop in case unused ids stop looking unused.
	maxID = 20000
	// maxConsecutiveErrors aborts a walk against a site that has stopped
	// answering, rather than spending the whole id range on failures.
	maxConsecutiveErrors = 24
	// bioPageSize is how many sets a model page shows; `sta` is the offset.
	bioPageSize = 4
	maxBioPages = 500
)

var siteBase = "https://nhlpcentral.com"

// Scraper implements scraper.StudioScraper for NHLP Central.
type Scraper struct {
	Client *http.Client
}

// New constructs an NHLP Central scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"nhlpcentral.com",
		"nhlpcentral.com/bio.php?name={model}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?nhlpcentral\.com(?:[/?#]|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func sceneURL(id int) string {
	return fmt.Sprintf("%s/update.php?id=%d", siteBase, id)
}

// modelName returns the model a /bio.php?name=X URL is filtered to.
func modelName(studioURL string) (string, bool) {
	u, err := url.Parse(studioURL)
	if err != nil || !strings.EqualFold(u.Path, "/bio.php") {
		return "", false
	}
	name := strings.Join(strings.Fields(u.Query().Get("name")), " ")
	return name, name != ""
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	w := &walker{s: s, studioURL: studioURL, delay: opts.Delay, now: time.Now().UTC(), workers: opts.Workers}
	if w.workers <= 0 {
		w.workers = defaultWorkers
	}

	if name, ok := modelName(studioURL); ok {
		scraper.Debugf(1, "%s: scraping model page for %q", siteID, name)
		s.runModel(ctx, w, name, opts.KnownIDs, out)
		return
	}
	s.runCatalogue(ctx, w, opts.KnownIDs, out)
}

func (s *Scraper) runCatalogue(ctx context.Context, w *walker, known map[string]bool, out chan<- scraper.SceneResult) {
	emitted := map[int]bool{}

	if len(known) > 0 {
		ids, err := s.latestIDs(ctx)
		switch {
		case err != nil:
			scraper.Debugf(1, "%s: homepage unavailable (%v), walking the whole catalogue", siteID, err)
		case len(ids) == 0:
			scraper.Debugf(1, "%s: no updates on the homepage, walking the whole catalogue", siteID)
		default:
			fresh, hit := untilKnown(ids, known)
			for i, p := range w.fetchAll(ctx, fresh) {
				if ctx.Err() != nil {
					return
				}
				emitted[fresh[i]] = true
				if !w.emit(ctx, out, p, false) {
					return
				}
			}
			if hit {
				scraper.Debugf(1, "%s: hit known ID among the latest updates, stopping early", siteID)
				send(ctx, out, scraper.StoppedEarly())
				return
			}
			scraper.Debugf(1, "%s: none of the %d latest updates is known, walking the whole catalogue", siteID, len(ids))
		}
	}

	w.walk(ctx, emitted, out)
}

func untilKnown(ids []int, known map[string]bool) ([]int, bool) {
	for i, id := range ids {
		if known[strconv.Itoa(id)] {
			return ids[:i], true
		}
	}
	return ids, false
}

func (s *Scraper) runModel(ctx context.Context, w *walker, name string, known map[string]bool, out chan<- scraper.SceneResult) {
	var ids []int
	seen := map[int]bool{}
	hit := false

	for page := 0; page < maxBioPages && !hit; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 0 && w.delay > 0 {
			select {
			case <-time.After(w.delay):
			case <-ctx.Done():
				return
			}
		}
		q := url.Values{"name": {name}, "sta": {strconv.Itoa(page * bioPageSize)}}
		pageURL := siteBase + "/bio.php?" + q.Encode()
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			send(ctx, out, scraper.Error(err))
			return
		}
		pageIDs, more, err := parseBioPage(string(body))
		if err != nil {
			send(ctx, out, scraper.Error(scraper.ParseError(pageURL, err)))
			return
		}
		for _, id := range pageIDs {
			if known[strconv.Itoa(id)] {
				hit = true
				break
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if !more || len(pageIDs) == 0 {
			break
		}
	}

	if len(ids) == 0 && !hit {
		send(ctx, out, scraper.Error(fmt.Errorf("%s: no sets listed for model %q", siteID, name)))
		return
	}

	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(ids), w.workers)
	if !send(ctx, out, scraper.Progress(len(ids))) {
		return
	}
	for _, p := range w.fetchAll(ctx, ids) {
		if ctx.Err() != nil || !w.emit(ctx, out, p, true) {
			return
		}
	}
	if hit {
		scraper.Debugf(1, "%s: hit known ID on the model page, stopping early", siteID)
		send(ctx, out, scraper.StoppedEarly())
	}
}

// ---- id walk ----

type probeState int

const (
	stateMissing probeState = iota
	statePhotoOnly
	stateScene
	stateError
)

type probe struct {
	id    int
	state probeState
	scene models.Scene
	err   error
}

type walker struct {
	s         *Scraper
	studioURL string
	delay     time.Duration
	now       time.Time
	workers   int
}

// emit sends one probe's outcome. listed marks an id taken from a listing,
// where an unused id means a listed set could not be collected.
func (w *walker) emit(ctx context.Context, out chan<- scraper.SceneResult, p probe, listed bool) bool {
	switch p.state {
	case stateScene:
		return send(ctx, out, scraper.Scene(p.scene))
	case stateError:
		return send(ctx, out, scraper.Error(p.err))
	case stateMissing:
		if listed {
			return send(ctx, out, scraper.Error(fmt.Errorf("%s: listed set %d has no page", sceneURL(p.id), p.id)))
		}
	case statePhotoOnly:
	}
	return true
}

func (w *walker) walk(ctx context.Context, emitted map[int]bool, out chan<- scraper.SceneResult) {
	gap, errRun := 0, 0
	for start := 1; start <= maxID; start += batchSize {
		if ctx.Err() != nil {
			return
		}
		end := min(start+batchSize, maxID+1)
		ids := make([]int, 0, end-start)
		for id := start; id < end; id++ {
			ids = append(ids, id)
		}

		for _, p := range w.fetchAll(ctx, ids) {
			if ctx.Err() != nil {
				return
			}
			switch p.state {
			case stateMissing:
				gap++
				errRun = 0
				continue
			case stateError:
				errRun++
				if !send(ctx, out, scraper.Error(p.err)) {
					return
				}
				if errRun >= maxConsecutiveErrors {
					send(ctx, out, scraper.Error(fmt.Errorf("%s: %d consecutive failed requests at id %d, aborting the walk", siteID, errRun, p.id)))
					return
				}
				continue
			case statePhotoOnly, stateScene:
			}
			gap, errRun = 0, 0
			if p.state == stateScene && !emitted[p.id] {
				if !send(ctx, out, scraper.Scene(p.scene)) {
					return
				}
			}
		}

		if gap >= maxGap {
			scraper.Debugf(1, "%s: %d consecutive unused ids by %d, stopping", siteID, gap, end-1)
			return
		}
	}
	send(ctx, out, scraper.Error(fmt.Errorf("%s: id walk reached the %d backstop without running out of sets", siteID, maxID)))
}

// fetchAll probes ids concurrently and returns the results in id order. The
// caller must check ctx before trusting them.
func (w *walker) fetchAll(ctx context.Context, ids []int) []probe {
	res := make([]probe, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, w.workers)
	for i, id := range ids {
		res[i].id = id
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if w.delay > 0 {
				select {
				case <-time.After(w.delay):
				case <-ctx.Done():
					return
				}
			}
			res[i] = w.s.fetchScene(ctx, id, w.studioURL, w.now)
		}()
	}
	wg.Wait()
	return res
}

func (s *Scraper) fetchScene(ctx context.Context, id int, studioURL string, now time.Time) probe {
	pageURL := sceneURL(id)
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return probe{id: id, state: stateError, err: err}
	}
	sc, state, err := parseScene(string(body), id, pageURL, studioURL, now)
	if err != nil {
		return probe{id: id, state: stateError, err: scraper.ParseError(pageURL, err)}
	}
	return probe{id: id, state: state, scene: sc}
}

// ---- parsing ----

var (
	setTitleRe = regexp.MustCompile(`id="fittext1b" style="color:#000;">([^<]*)</div><br>`)
	updatedRe  = regexp.MustCompile(`Updated:\s*([^<]+)`)
	modelRe    = regexp.MustCompile(`(?s)Other sets of\s+(.*?)\s+on site:`)
	descRe     = regexp.MustCompile(`(?s)id="fittext4" style="color:#000;">(.*?)</div>`)
	videoRe    = regexp.MustCompile(`Video:\s*(\d{1,2}:\d{2}(?::\d{2})?)`)
	videoLnRe  = regexp.MustCompile(`Video:`)
	fileRe     = regexp.MustCompile(`/images/(?:mp4|wmv|mov|avi|flv|m4v)\w*\.jpg`)
	thumbRe    = regexp.MustCompile(`src="((?:https?:)?//[^"]*/awizicon/\d+_\d+\.jpg)"`)
	tagsRe     = regexp.MustCompile(`(?s)Tags in this set:\s*(.*?)\s*</div>`)
	creditRe   = regexp.MustCompile(`^(.+?)(?:\s+-\s*|\s*-\s+)(.+)$`)
	wordRe     = regexp.MustCompile(`[a-z0-9]+`)
)

var errNoTitle = errors.New("set page has no title")

// parseScene reads one /update.php page. An unused id serves no MODEL block
// at all; a set that ships only stills has no video duration and no video
// file rows, and is reported as photo-only so the walk can skip it.
func parseScene(body string, id int, pageURL, studioURL string, now time.Time) (models.Scene, probeState, error) {
	if !strings.Contains(body, "MODEL:") {
		return models.Scene{}, stateMissing, nil
	}
	var fullTitle string
	for _, m := range setTitleRe.FindAllStringSubmatch(body, -1) {
		t := strings.TrimSpace(m[1])
		if t != "" && !strings.HasPrefix(t, "Updated:") {
			fullTitle = clean(t)
			break
		}
	}
	if fullTitle == "" {
		return models.Scene{}, stateError, errNoTitle
	}
	if !videoLnRe.MatchString(body) && !fileRe.MatchString(body) {
		return models.Scene{}, statePhotoOnly, nil
	}

	sc := models.Scene{
		ID:        strconv.Itoa(id),
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       pageURL,
		ScrapedAt: now,
	}

	var model string
	if m := modelRe.FindStringSubmatch(body); m != nil {
		model = clean(m[1])
	}
	if model != "" {
		sc.Performers = []string{model}
	}
	sc.Title = stripCredit(fullTitle, model)

	if m := updatedRe.FindStringSubmatch(body); m != nil {
		if t, err := parseutil.TryParseDate(strings.TrimSpace(m[1]), "02 January 2006", "2 January 2006"); err == nil {
			sc.Date = t.UTC()
		}
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(m[1])
	}
	if m := videoRe.FindStringSubmatch(body); m != nil {
		sc.Duration = parseutil.ParseDurationColon(m[1])
	}
	if m := thumbRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = m[1]
		if strings.HasPrefix(sc.Thumbnail, "//") {
			sc.Thumbnail = "https:" + sc.Thumbnail
		}
	}
	if m := tagsRe.FindStringSubmatch(body); m != nil {
		sc.Tags = splitTags(m[1])
	}
	sc.Studio = studioFor(sc.Tags)
	return sc, stateScene, nil
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

// stripCredit drops a leading "Model - " credit from a set title. The prefix
// is only treated as a credit when it shares a word with the site's model
// name, since it is often a nickname ("Kiana - …" for Kiana Kraze) and a title
// can contain a dash of its own.
func stripCredit(title, model string) string {
	m := creditRe.FindStringSubmatch(title)
	if m == nil || model == "" {
		return title
	}
	names := map[string]bool{}
	for _, w := range wordRe.FindAllString(strings.ToLower(model), -1) {
		if len(w) > 1 {
			names[w] = true
		}
	}
	for _, w := range wordRe.FindAllString(strings.ToLower(m[1]), -1) {
		if names[w] {
			return strings.TrimSpace(m[2])
		}
	}
	return title
}

func splitTags(raw string) []string {
	var tags []string
	seen := map[string]bool{}
	for _, t := range strings.Split(raw, ",") {
		t = clean(t)
		k := strings.ToLower(t)
		if t == "" || seen[k] {
			continue
		}
		seen[k] = true
		tags = append(tags, t)
	}
	return tags
}

// brandTags are the tags NHLP uses to mark which of its former sites a set
// came from.
var brandTags = map[string]bool{
	"stocking_queens": true, "ph4u": true, "phflash": true, "academy": true,
	"joy_of_feet": true, "sq_archive": true, "q_archive": true, "mark_lacy": true,
	"harmony point": true, "secrets in lace": true,
}

// studioFor credits Pantyhosed4U only when PH4U is the set's sole brand tag;
// the other brand tags either overlap each other or have no matching studio.
func studioFor(tags []string) string {
	ph4u, others := false, false
	for _, t := range tags {
		k := strings.ToLower(t)
		switch {
		case k == "ph4u":
			ph4u = true
		case brandTags[k]:
			others = true
		}
	}
	if ph4u && !others {
		return "Pantyhosed4U"
	}
	return studioName
}

var (
	latestRe = regexp.MustCompile(`update\.php\?id=(\d+)`)
	bioIDRe  = regexp.MustCompile(`/awizicon/(\d+)_\d+\.jpg`)
	bioMore  = regexp.MustCompile(`bio\.php\?name=[^"]*&(?:amp;)?sta=\d+`)
)

// latestIDs returns the homepage's update ids, newest first.
func (s *Scraper) latestIDs(ctx context.Context) ([]int, error) {
	body, err := s.fetchPage(ctx, siteBase+"/")
	if err != nil {
		return nil, err
	}
	return uniqueIDs(latestRe, string(body)), nil
}

func uniqueIDs(re *regexp.Regexp, body string) []int {
	var ids []int
	seen := map[int]bool{}
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		id, err := strconv.Atoi(m[1])
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// parseBioPage returns the set ids in a model page's set block and whether a
// "MORE" link follows. A model with no sets still renders the block, empty.
func parseBioPage(body string) ([]int, bool, error) {
	i := strings.Index(body, "<!--u -->")
	if i < 0 {
		return nil, false, errors.New("model page has no set block")
	}
	block := body[i:]
	if j := strings.Index(block, "</table>"); j >= 0 {
		block = block[:j]
	}
	return uniqueIDs(bioIDRe, block), bioMore.MatchString(body[i:]), nil
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
