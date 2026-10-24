// Package mysatin scrapes the MySatin Productions tours (mysatin.com,
// satinplay.com), which share one hand-rolled CMS.
//
// The public updates listing (`/updates`, then `/updates{N}.html`) shows six
// sets a page but stops at page 15 — later page numbers serve page 15 again —
// so it reaches back only about 90 sets. Each model profile lists that model's
// sets, which reaches the back catalogue, so a full walk also visits every
// profile under `/models`. Performers appear only on a
// set's own preview page, so every set is fetched once; its date is taken from
// the listing, or else from the `YYYY-MM-DD_slug` gallery path.
package mysatin

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

// SiteConfig describes one MySatin Productions tour.
type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

var sites = []SiteConfig{
	{SiteID: "mysatin", Domain: "mysatin.com", StudioName: "MySatin"},
	{SiteID: "satinplay", Domain: "satinplay.com", StudioName: "SatinPlay"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	d := s.cfg.Domain
	return []string{d, d + "/updates", d + "/models", d + "/models/{name}"}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type setRef struct {
	id    string
	path  string
	date  string
	title string
}

var modelURLRe = regexp.MustCompile(`^https?://[^/]+/models/([^/?#]+)`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	var refs []setRef
	var ok bool
	if m := modelURLRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping model page %s", s.cfg.SiteID, m[1])
		refs, ok = s.collectModels(ctx, []string{m[1]}, map[string]bool{}, nil, opts, out)
	} else {
		scraper.Debugf(1, "%s: scraping full catalogue", s.cfg.SiteID)
		refs, ok = s.collectCatalogue(ctx, opts, out)
	}
	if !ok || ctx.Err() != nil {
		return
	}
	s.fetchDetails(ctx, studioURL, refs, opts, out)
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func updatesPath(page int) string {
	if page <= 1 {
		return "/updates"
	}
	return fmt.Sprintf("/updates%d.html", page)
}

// collectCatalogue walks the updates listing and then every model profile.
// A known ID ends the walk after its page and skips the profiles, since new
// sets always appear on the first updates page.
func (s *Scraper) collectCatalogue(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) ([]setRef, bool) {
	seen := make(map[string]bool)
	var refs []setRef
	stopped := false

	for page := 1; ; page++ {
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return refs, false
		}
		pageURL := s.base + updatesPath(page)
		scraper.Debugf(1, "%s: fetching updates page %d", s.cfg.SiteID, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			send(ctx, out, scraper.Error(fmt.Errorf("updates page %d: %w", page, err)))
			return refs, false
		}
		items := parseUpdates(body)
		if len(items) == 0 {
			if page == 1 {
				send(ctx, out, scraper.Error(scraper.ParseError(pageURL, fmt.Errorf("no sets found on updates page"))))
				return refs, false
			}
			break
		}
		fresh := 0
		for _, it := range items {
			if seen[it.id] {
				continue
			}
			seen[it.id] = true
			fresh++
			if opts.KnownIDs[it.id] {
				stopped = true
				continue
			}
			refs = append(refs, it)
		}
		// Past the last page the site serves the last page again.
		if fresh == 0 || stopped || !strings.Contains(string(body), updatesPath(page+1)) {
			break
		}
	}

	if stopped {
		scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
		if !send(ctx, out, scraper.StoppedEarly()) {
			return refs, false
		}
		return refs, true
	}

	if !scraper.Pace(ctx, opts.Delay) {
		return refs, false
	}
	modelsURL := s.base + "/models"
	body, err := s.fetchPage(ctx, modelsURL)
	if err != nil {
		send(ctx, out, scraper.Error(fmt.Errorf("models index: %w", err)))
		return refs, false
	}
	slugs := parseModelIndex(body)
	if len(slugs) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(modelsURL, fmt.Errorf("no models found"))))
		return refs, false
	}
	scraper.Debugf(1, "%s: walking %d model profiles", s.cfg.SiteID, len(slugs))
	return s.collectModels(ctx, slugs, seen, refs, opts, out)
}

func (s *Scraper) collectModels(ctx context.Context, slugs []string, seen map[string]bool, refs []setRef, opts scraper.ListOpts, out chan<- scraper.SceneResult) ([]setRef, bool) {
	for i, slug := range slugs {
		if (i > 0 || len(seen) > 0) && !scraper.Pace(ctx, opts.Delay) {
			return refs, false
		}
		pageURL := s.base + "/models/" + slug
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			if !send(ctx, out, scraper.Error(fmt.Errorf("model %s: %w", slug, err))) {
				return refs, false
			}
			continue
		}
		for _, it := range parseModelSets(body) {
			if seen[it.id] || opts.KnownIDs[it.id] {
				continue
			}
			seen[it.id] = true
			refs = append(refs, it)
		}
	}
	return refs, true
}

func (s *Scraper) fetchDetails(ctx context.Context, studioURL string, refs []setRef, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(refs) == 0 {
		return
	}
	if !send(ctx, out, scraper.Progress(len(refs))) {
		return
	}
	workers := scraper.WorkerCount(opts, 3)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(refs), workers)

	work := make(chan setRef)
	var wg sync.WaitGroup
	now := time.Now().UTC()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				detailURL := s.base + ref.path
				body, err := s.fetchPage(ctx, detailURL)
				var r scraper.SceneResult
				if err != nil {
					r = scraper.Error(fmt.Errorf("set %s: %w", ref.id, err))
				} else if d, ok := parseDetail(body); !ok {
					r = scraper.Error(scraper.ParseError(detailURL, fmt.Errorf("no <h1> on set page")))
				} else if d.title == "" && ref.title == "" {
					// The site itself publishes a few sets with an empty
					// title everywhere; there is nothing to store them under.
					r = scraper.Error(scraper.AbsentError(detailURL, fmt.Errorf("site publishes no title for set %s", ref.id)))
				} else {
					r = scraper.Scene(s.toScene(ref, d, studioURL, now))
				}
				if !send(ctx, out, r) {
					return
				}
			}
		}()
	}
	for _, ref := range refs {
		select {
		case work <- ref:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(work)
	wg.Wait()
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- parsing ----

var (
	setLinkRe    = regexp.MustCompile(`href="(/[a-z-]+-gallery/([A-Za-z0-9_-]+)\.html)"`)
	photosetRe   = regexp.MustCompile(`<div class="photoset">`)
	timeRe       = regexp.MustCompile(`<time datetime="(\d{4}-\d{2}-\d{2})"`)
	spanTitleRe  = regexp.MustCompile(`(?s)</time>:\s*<span>(.*?)</span>`)
	setTitleRe   = regexp.MustCompile(`(?s)<td class="settitle"><a [^>]*>(.*?)</a>`)
	setDivRe     = regexp.MustCompile(`<div class="set">`)
	modelSetRe   = regexp.MustCompile(`<div class="modelrecentset">`)
	setDateRe    = regexp.MustCompile(`<td class="setdate">(\d{4}-\d{2}-\d{2})</td>`)
	modelLinkRe  = regexp.MustCompile(`href="/models/([^"/?#]+)"`)
	h1Re         = regexp.MustCompile(`(?s)<h1>(.*?)</h1>`)
	plainPRe     = regexp.MustCompile(`(?s)<p>(.*?)</p>`)
	modelsCellRe = regexp.MustCompile(`(?s)Models:</td>\s*<td>(.*?)</td>`)
	anchorTextRe = regexp.MustCompile(`(?s)<a [^>]*>(.*?)</a>`)
	videoRe      = regexp.MustCompile(`(?s)Video:</td>\s*<td>([^<]+)`)
	galleryRe    = regexp.MustCompile(`src="(/gallery/\d{4}/\d{2}/(\d{4}-\d{2}-\d{2})_[^/"]+/preview/video-preview\.jpg)"`)
	hoursRe      = regexp.MustCompile(`(\d+)\s*hours?`)
	minsRe       = regexp.MustCompile(`(\d+)\s*mins?`)
	tagStripRe   = regexp.MustCompile(`<[^>]*>`)
)

// blocks splits body at each match of start.
func blocks(body []byte, start *regexp.Regexp) [][]byte {
	locs := start.FindAllIndex(body, -1)
	res := make([][]byte, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		res = append(res, body[loc[0]:end])
	}
	return res
}

func parseUpdates(body []byte) []setRef {
	var refs []setRef
	for _, b := range blocks(body, photosetRe) {
		m := setLinkRe.FindSubmatch(b)
		if m == nil {
			continue
		}
		ref := setRef{path: string(m[1]), id: string(m[2])}
		if d := timeRe.FindSubmatch(b); d != nil {
			ref.date = string(d[1])
		}
		if t := spanTitleRe.FindSubmatch(b); t != nil {
			ref.title = cleanText(string(t[1]))
		}
		refs = append(refs, ref)
	}
	return refs
}

func parseModelSets(body []byte) []setRef {
	var refs []setRef
	for _, b := range blocks(body, modelSetRe) {
		m := setLinkRe.FindSubmatch(b)
		if m == nil {
			continue
		}
		ref := setRef{path: string(m[1]), id: string(m[2])}
		if d := setDateRe.FindSubmatch(b); d != nil {
			ref.date = string(d[1])
		}
		if t := setTitleRe.FindSubmatch(b); t != nil {
			ref.title = cleanText(string(t[1]))
		}
		refs = append(refs, ref)
	}
	return refs
}

func parseModelIndex(body []byte) []string {
	var slugs []string
	seen := make(map[string]bool)
	for _, m := range modelLinkRe.FindAllSubmatch(body, -1) {
		slug := string(m[1])
		if !seen[slug] {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
	}
	return slugs
}

type detail struct {
	title       string
	description string
	performers  []string
	duration    int
	thumbnail   string
	date        string
}

func parseDetail(body []byte) (detail, bool) {
	var d detail
	loc := h1Re.FindSubmatchIndex(body)
	if loc == nil {
		return d, false
	}
	d.title = cleanText(string(body[loc[2]:loc[3]]))
	// Archived sets put a classed `<p class="archived">` notice before the
	// description, which is the first plain paragraph ahead of the set block.
	intro := body[loc[1]:]
	if end := setDivRe.FindIndex(intro); end != nil {
		intro = intro[:end[0]]
	}
	if p := plainPRe.FindSubmatch(intro); p != nil {
		d.description = cleanText(tagStripRe.ReplaceAllString(string(p[1]), " "))
	}
	if c := modelsCellRe.FindSubmatch(body); c != nil {
		for _, a := range anchorTextRe.FindAllSubmatch(c[1], -1) {
			if n := cleanText(string(a[1])); n != "" {
				d.performers = append(d.performers, n)
			}
		}
	}
	if v := videoRe.FindSubmatch(body); v != nil {
		d.duration = parseMinutes(string(v[1]))
	}
	if g := galleryRe.FindSubmatch(body); g != nil {
		d.thumbnail = string(g[1])
		d.date = string(g[2])
	}
	return d, true
}

func parseMinutes(s string) int {
	total := 0
	if m := hoursRe.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		total += h * 3600
	}
	if m := minsRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		total += n * 60
	}
	return total
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

func (s *Scraper) toScene(ref setRef, d detail, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          ref.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Title:       d.title,
		URL:         s.base + ref.path,
		Description: d.description,
		Performers:  d.performers,
		Studio:      s.cfg.StudioName,
		Duration:    d.duration,
		ScrapedAt:   now,
	}
	if sc.Title == "" {
		sc.Title = ref.title
	}
	if d.thumbnail != "" {
		sc.Thumbnail = s.base + d.thumbnail
	}
	date := ref.date
	if date == "" {
		date = d.date
	}
	if t, err := parseutil.TryParseDate(date, "2006-01-02"); err == nil {
		sc.Date = t
	}
	return sc
}
