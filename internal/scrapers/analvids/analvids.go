package analvids

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
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://www.analvids.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "analvids" }

func (s *Scraper) Patterns() []string {
	return []string{
		"analvids.com",
		"analvids.com/new-videos",
		"analvids.com/studios/{slug}",
		"analvids.com/model/{id}/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?analvids\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardSplitRe = regexp.MustCompile(`<div class="card-scene[^"]*"\s*data-content="(\d+)">`)
	cardIDRe    = regexp.MustCompile(`data-content="(\d+)"`)

	cardHrefRe  = regexp.MustCompile(`<a href="([^"]*?/watch/\d+/[^"]*)"`)
	cardTitleRe = regexp.MustCompile(`alt="([^"]*)"`)
	cardThumbRe = regexp.MustCompile(`data-src="([^"]*)"`)
	cardDurRe   = regexp.MustCompile(`label--time">([^<]*\bmin\b[^<]*)</div>`)
	cardDateRe  = regexp.MustCompile(`label--time">(\d{4}-\d{2}-\d{2})</div>`)

	detailPerfRe   = regexp.MustCompile(`href="https?://www\.analvids\.com/model/\d+/[^"]*"[^>]*>([^<]+)</a>`)
	detailDateRe   = regexp.MustCompile(`bi-calendar3.*?</i>\s*(\d{4}-\d{2}-\d{2})`)
	detailDurRe    = regexp.MustCompile(`bi-clock.*?</i>\s*(\d+:\d+)`)
	detailTagRe    = regexp.MustCompile(`<a[^>]*href="/genre/[^"]*"[^>]*>([^<]+)</a>`)
	detailStudioRe = regexp.MustCompile(`(?s)Studio:.*?<a href="/studios/[^"]*">([^<]+)</a>`)

	studioSlugRe = regexp.MustCompile(`/studios/([^/]+)`)
	modelPathRe  = regexp.MustCompile(`/model/(\d+/[^/]+)`)

	durationHoursRe = regexp.MustCompile(`(\d+)\s*h`)
	durationMinsRe  = regexp.MustCompile(`(\d+)\s*min`)
)

type listEntry struct {
	id    string
	url   string
	title string
	thumb string
	dur   int
	date  string
}

func parseListing(body string) []listEntry {
	locs := cardSplitRe.FindAllStringIndex(body, -1)
	if len(locs) == 0 {
		return nil
	}
	out := make([]listEntry, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		card := body[loc[0]:end]
		idMatch := cardIDRe.FindStringSubmatch(card)
		if idMatch == nil {
			continue
		}
		e := listEntry{id: idMatch[1]}
		if v := cardHrefRe.FindStringSubmatch(card); v != nil {
			e.url = html.UnescapeString(v[1])
		}
		if v := cardTitleRe.FindStringSubmatch(card); v != nil {
			e.title = html.UnescapeString(v[1])
		}
		if v := cardThumbRe.FindStringSubmatch(card); v != nil {
			e.thumb = html.UnescapeString(v[1])
		}
		if v := cardDurRe.FindStringSubmatch(card); v != nil {
			e.dur = parseDuration(v[1])
		}
		if v := cardDateRe.FindStringSubmatch(card); v != nil {
			e.date = v[1]
		}
		if e.id != "" {
			out = append(out, e)
		}
	}
	return out
}

func parseDuration(s string) int {
	s = strings.TrimSpace(s)
	var total int
	if m := durationHoursRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		total += n * 3600
	}
	if m := durationMinsRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		total += n * 60
	}
	return total
}

type detailData struct {
	performers []string
	tags       []string
	studio     string
	date       string
	duration   int
}

func parseDetail(body string) detailData {
	d := detailData{}
	seen := make(map[string]bool)
	for _, m := range detailPerfRe.FindAllStringSubmatch(body, -1) {
		name := strings.TrimSpace(html.UnescapeString(m[1]))
		if name != "" && !seen[name] {
			seen[name] = true
			d.performers = append(d.performers, name)
		}
	}
	if m := detailDateRe.FindStringSubmatch(body); m != nil {
		d.date = m[1]
	}
	if m := detailDurRe.FindStringSubmatch(body); m != nil {
		d.duration = parseutil.ParseDurationColon(m[1])
	}
	tagSeen := make(map[string]bool)
	for _, m := range detailTagRe.FindAllStringSubmatch(body, -1) {
		tag := strings.TrimSpace(html.UnescapeString(m[1]))
		if tag != "" && !tagSeen[tag] {
			tagSeen[tag] = true
			d.tags = append(d.tags, tag)
		}
	}
	if m := detailStudioRe.FindStringSubmatch(body); m != nil {
		d.studio = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	return d
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	pathBase, dateSorted := s.resolveMode(studioURL)

	seen := make(map[string]bool)

	// Only pass KnownIDs when date-sorted; non-date-sorted modes
	// (studio/model) don't support early-stop.
	paginateOpts := opts
	if !dateSorted {
		paginateOpts.KnownIDs = nil
	}

	scraper.Paginate(ctx, paginateOpts, "analvids", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := pathBase
		if page > 1 {
			u += "/" + strconv.Itoa(page)
		}

		body, err := s.fetch(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}

		entries := parseListing(body)
		if len(entries) == 0 {
			return scraper.PageResult{}, nil
		}

		// Dedup: if every entry on this page was already seen on a
		// previous page, the site is looping — stop.
		allSeen := true
		for _, e := range entries {
			if !seen[e.id] {
				allSeen = false
				break
			}
		}
		if allSeen {
			return scraper.PageResult{}, nil
		}

		var work []listEntry
		for _, e := range entries {
			if seen[e.id] {
				continue
			}
			seen[e.id] = true
			work = append(work, e)
		}

		results := s.fetchDetails(ctx, work, opts.Delay, studioURL)

		var scenes []models.Scene
		for _, sc := range results {
			if sc.Err != nil {
				select {
				case out <- scraper.Error(sc.Err):
				case <-ctx.Done():
				}
				continue
			}
			scenes = append(scenes, sc.Scene)
		}
		// If every detail fetch on this page failed, scenes is empty while the
		// page genuinely had entries; keep paginating (the terminal page is the
		// empty/all-seen early return above).
		return scraper.PageResult{Scenes: scenes, Continue: len(work) > 0}, nil
	})
}

type sceneOrErr struct {
	Scene models.Scene
	Err   error
}

func (s *Scraper) fetchDetails(ctx context.Context, entries []listEntry, delay time.Duration, studioURL string) []sceneOrErr {
	results := make([]sceneOrErr, len(entries))
	var wg sync.WaitGroup

	sem := make(chan struct{}, 4)
	for i, e := range entries {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, entry listEntry) {
			defer wg.Done()
			defer func() { <-sem }()

			if !scraper.Pace(ctx, delay) {
				return
			}

			body, err := s.fetch(ctx, entry.url)
			if err != nil {
				results[idx] = sceneOrErr{Err: fmt.Errorf("detail %s: %w", entry.id, err)}
				return
			}

			detail := parseDetail(body)
			results[idx] = sceneOrErr{Scene: buildScene(entry, detail, studioURL)}
		}(i, e)
	}
	wg.Wait()
	return results
}

func buildScene(e listEntry, d detailData, studioURL string) models.Scene {
	sc := models.Scene{
		ID:         e.id,
		SiteID:     "analvids",
		StudioURL:  studioURL,
		Title:      e.title,
		URL:        e.url,
		Thumbnail:  e.thumb,
		Performers: d.performers,
		Tags:       d.tags,
		Studio:     d.studio,
		ScrapedAt:  time.Now().UTC(),
	}
	if d.duration > 0 {
		sc.Duration = d.duration
	} else {
		sc.Duration = e.dur
	}
	dateStr := d.date
	if dateStr == "" {
		dateStr = e.date
	}
	if dateStr != "" {
		if t, err := time.Parse("2006-01-02", dateStr); err == nil {
			sc.Date = t.UTC()
		}
	}
	return sc
}

func (s *Scraper) resolveMode(studioURL string) (pathBase string, dateSorted bool) {
	if m := studioSlugRe.FindStringSubmatch(studioURL); m != nil {
		return s.base + "/studios/" + m[1], false
	}
	if m := modelPathRe.FindStringSubmatch(studioURL); m != nil {
		return s.base + "/model/" + m[1], false
	}
	return s.base + "/new-videos", true
}

func (s *Scraper) fetch(ctx context.Context, u string) (string, error) {
	if !strings.HasPrefix(u, "http") {
		u = s.base + u
	}
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	b, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading body: %w", err)
	}
	return string(b), nil
}
