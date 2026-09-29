// Package handdomination scrapes handdomination.com, whose whole catalogue sits
// on one un-paginated browse page. See docs/scrapers.md.
package handdomination

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
	siteID     = "handdomination"
	studioName = "Hand Domination"
	// The site answers on http only; https does not connect at all.
	defaultBase = "http://www.handdomination.com"
	// detailWorkers caps the detail pool. The catalogue is ~212 scenes and the
	// listing is a single request, so this is the whole cost of a run.
	detailWorkers = 4
)

var (
	matchRe   = regexp.MustCompile(`^https?://(?:www\.)?handdomination\.com(?:/|$)`)
	modelIDRe = regexp.MustCompile(`model_bio\.php\?model_id=(\d+)`)

	cardSplitRe = regexp.MustCompile(`<div class="browse_updates">`)
	cardVideoRe = regexp.MustCompile(`(?s)<a href="video\.php\?video_id=(\d+)">\s*<img[^>]+src="([^"]+)"`)
	cardTitleRe = regexp.MustCompile(`(?s)</a></li>\s*<li>(.*?)</li>`)
	cardModelRe = regexp.MustCompile(`(?s)<a href="model_bio\.php\?model_id=(\d+)\s*">(.*?)</a>`)
	cardDateRe  = regexp.MustCompile(`date:\s*([^<]+?)\s*</`)
	cardDurRe   = regexp.MustCompile(`duration:\s*(\d+:\d+(?::\d+)?)`)

	detailTitleRe  = regexp.MustCompile(`(?s)<h3 class="videoTitle">(.*?)</h3>`)
	detailCatsRe   = regexp.MustCompile(`(?s)<li class="videoCategories">(.*?)</li>`)
	detailDescRe   = regexp.MustCompile(`(?s)<li class="videoDescription"><p>(.*?)</p>`)
	detailPosterRe = regexp.MustCompile(`poster="([^"]+)"`)
	anchorTextRe   = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	tagStripRe     = regexp.MustCompile(`<[^>]+>`)
)

// dateLayouts covers the listing's "Jun 17, 2019", whose day is zero-padded on
// some rows and not on others.
var dateLayouts = []string{"Jan 2, 2006", "Jan 02, 2006"}

// Scraper implements scraper.StudioScraper for handdomination.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Hand Domination scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: defaultBase}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"handdomination.com",
		"handdomination.com/model_bio.php?model_id={id}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// entry is one listing card. The card's title is truncated, so the detail page
// is the authority for it.
type entry struct {
	id       string
	thumb    string
	title    string
	models   []string
	modelIDs []string
	date     string
	duration string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	body, err := s.fetch(ctx, s.base+"/browse_video.php")
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	entries := parseListing(body)
	if len(entries) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(s.base+"/browse_video.php",
			fmt.Errorf("no update cards on the browse page"))))
		return
	}

	// A model page lists only a couple of that model's scenes, so the mode
	// filters the full listing on the model id the cards already carry.
	if m := modelIDRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: filtering listing to model %s", siteID, m[1])
		entries = filterByModel(entries, m[1])
		if len(entries) == 0 {
			send(ctx, out, scraper.Error(scraper.AbsentError(studioURL,
				fmt.Errorf("model %s credits no scene in the listing", m[1]))))
			return
		}
	}

	scraper.Debugf(1, "%s: %d scenes on the browse page", siteID, len(entries))
	if !send(ctx, out, scraper.Progress(len(entries))) {
		return
	}

	// The listing is date-descending, so the first known id ends the run.
	fresh := entries
	stopped := false
	for i, e := range entries {
		if opts.KnownIDs[e.id] {
			fresh, stopped = entries[:i], true
			break
		}
	}

	scenes := s.enrich(ctx, studioURL, fresh, opts, out)
	for _, sc := range scenes {
		if !send(ctx, out, scraper.Scene(sc)) {
			return
		}
	}
	if stopped {
		scraper.Debugf(1, "%s: hit known ID, stopping early", siteID)
		send(ctx, out, scraper.StoppedEarly())
	}
}

func filterByModel(entries []entry, modelID string) []entry {
	var kept []entry
	for _, e := range entries {
		for _, id := range e.modelIDs {
			if id == modelID {
				kept = append(kept, e)
				break
			}
		}
	}
	return kept
}

// parseListing reads the browse page's update cards. The blocks are sliced
// between opening markers: their closing </div> is shared with inner markup.
func parseListing(body string) []entry {
	marks := cardSplitRe.FindAllStringIndex(body, -1)
	entries := make([]entry, 0, len(marks))
	for i, m := range marks {
		end := len(body)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		block := body[m[1]:end]

		v := cardVideoRe.FindStringSubmatch(block)
		if v == nil {
			continue
		}
		e := entry{id: v[1], thumb: v[2]}
		if t := cardTitleRe.FindStringSubmatch(block); t != nil {
			e.title = cleanText(t[1])
		}
		for _, md := range cardModelRe.FindAllStringSubmatch(block, -1) {
			if name := cleanText(md[2]); name != "" {
				e.models = append(e.models, name)
				e.modelIDs = append(e.modelIDs, md[1])
			}
		}
		if d := cardDateRe.FindStringSubmatch(block); d != nil {
			e.date = cleanText(d[1])
		}
		if d := cardDurRe.FindStringSubmatch(block); d != nil {
			e.duration = d[1]
		}
		entries = append(entries, e)
	}
	return entries
}

// enrich fetches each scene's detail page for the untruncated title, the
// categories and the description. A failure keeps the listing-only record.
func (s *Scraper) enrich(ctx context.Context, studioURL string, entries []entry, opts scraper.ListOpts, out chan<- scraper.SceneResult) []models.Scene {
	workers := scraper.WorkerCount(opts, detailWorkers)
	scenes := make([]models.Scene, len(entries))
	now := time.Now().UTC()
	for i, e := range entries {
		scenes[i] = s.baseScene(e, studioURL, now)
	}
	if len(entries) == 0 {
		return scenes
	}

	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(entries), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range entries {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				send(ctx, out, scraper.Error(err))
				return
			}
			applyDetail(&scenes[i], body, s.base)
		}(i)
	}
	wg.Wait()
	return scenes
}

func (s *Scraper) baseScene(e entry, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:         e.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     studioName,
		Title:      e.title,
		URL:        fmt.Sprintf("%s/video.php?video_id=%s", s.base, e.id),
		Performers: e.models,
		Duration:   parseutil.ParseDurationColon(e.duration),
		ScrapedAt:  now,
	}
	if e.thumb != "" {
		sc.Thumbnail = resolveURL(s.base, e.thumb)
	}
	if t, err := parseutil.TryParseDate(e.date, dateLayouts...); err == nil {
		sc.Date = t
	}
	return sc
}

// applyDetail folds the detail page into a scene built from its listing card.
func applyDetail(sc *models.Scene, body, base string) {
	if t := detailTitleRe.FindStringSubmatch(body); t != nil {
		if title := cleanText(t[1]); title != "" {
			sc.Title = title
		}
	}
	if c := detailCatsRe.FindStringSubmatch(body); c != nil {
		for _, a := range anchorTextRe.FindAllStringSubmatch(c[1], -1) {
			if name := cleanText(a[1]); name != "" {
				sc.Tags = append(sc.Tags, name)
			}
		}
	}
	if d := detailDescRe.FindStringSubmatch(body); d != nil {
		sc.Description = cleanText(d[1])
	}
	if sc.Thumbnail == "" {
		if p := detailPosterRe.FindStringSubmatch(body); p != nil {
			sc.Thumbnail = resolveURL(base, p[1])
		}
	}
}

// resolveURL turns the site's relative "update/…" paths into absolute ones.
func resolveURL(base, ref string) string {
	switch {
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return ref
	case strings.HasPrefix(ref, "/"):
		return base + ref
	default:
		return base + "/" + ref
	}
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
