package rawfuckclub

import (
	"context"
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

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://www.rawfuckclub.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "rawfuckclub" }

func (s *Scraper) Patterns() []string {
	return []string{
		"rawfuckclub.com",
		"rawfuckclub.com/browse/new",
		"rawfuckclub.com/{username}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?rawfuckclub\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardSplitRe   = regexp.MustCompile(`<div class="[^"]*watch-slide watch-slide-new[^"]*"`)
	cardVideoRe   = regexp.MustCompile(`<a href="(/video/([A-Za-z0-9]+)-[^"]*)"[^>]*title="([^"]*)"`)
	cardThumbRe   = regexp.MustCompile(`videoPreviewDemo"[^>]*data-src="([^"]+)"`)
	cardChannelRe = regexp.MustCompile(`(?s)<div class="browse-channel-name">\s*<a[^>]*title="([^"]*)"`)

	detailDurRe      = regexp.MustCompile(`(?s)<div class="watch-duration[^"]*">\s*(\d+)\s*minutes?\s*</div>`)
	detailDateRe     = regexp.MustCompile(`(?s)<p class="watch-published-date"[^>]*>\s*(?:Posted|Reposted) on ([A-Z][a-z]+ \d+, \d{4})`)
	detailOrigDateRe = regexp.MustCompile(`Originally posted\s+on ([A-Z][a-z]+ \d+, \d{4})`)
	detailPerfRe     = regexp.MustCompile(`<span class="badge badge-primary">([^<]+)</span>`)
	detailTagRe      = regexp.MustCompile(`<span class="badge badge-secondary">([^<]+)</span>`)
	detailStudioRe   = regexp.MustCompile(`(?s)<div class="watch-channel-name"[^>]*>\s*<a[^>]*>([^<]+)</a>`)
	detailDescRe     = regexp.MustCompile(`(?s)<p class="watch-description">\s*(.*?)\s*</p>`)
	detailBuyRe      = regexp.MustCompile(`>Buy \$([0-9.]+)<`)
)

type listEntry struct {
	id      string
	url     string
	title   string
	thumb   string
	channel string
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
		m := cardVideoRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		e := listEntry{
			id:    m[2],
			url:   m[1],
			title: html.UnescapeString(m[3]),
		}
		if v := cardThumbRe.FindStringSubmatch(card); v != nil {
			e.thumb = v[1]
		}
		if v := cardChannelRe.FindStringSubmatch(card); v != nil {
			e.channel = html.UnescapeString(v[1])
		}
		out = append(out, e)
	}
	return out
}

type detailData struct {
	performers  []string
	tags        []string
	studio      string
	date        string
	duration    int
	description string
	buyPrice    float64
}

func parseDetail(body string) detailData {
	d := detailData{}
	if m := detailDurRe.FindStringSubmatch(body); m != nil {
		n, _ := strconv.Atoi(m[1])
		d.duration = n * 60
	}
	if m := detailOrigDateRe.FindStringSubmatch(body); m != nil {
		d.date = m[1]
	} else if m := detailDateRe.FindStringSubmatch(body); m != nil {
		d.date = m[1]
	}
	seen := make(map[string]bool)
	for _, m := range detailPerfRe.FindAllStringSubmatch(body, -1) {
		name := strings.TrimSpace(html.UnescapeString(m[1]))
		if name != "" && !seen[name] {
			seen[name] = true
			d.performers = append(d.performers, name)
		}
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
	if m := detailDescRe.FindStringSubmatch(body); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := detailBuyRe.FindStringSubmatch(body); m != nil {
		d.buyPrice, _ = strconv.ParseFloat(m[1], 64)
	}
	return d
}

var reservedPaths = map[string]bool{
	"browse": true, "video": true, "help": true, "channels": true,
	"login": true, "signup": true, "settings": true,
}

func (s *Scraper) resolveMode(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "/browse/new"
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" || path == "/browse/new" {
		return "/browse/new"
	}
	if strings.HasPrefix(path, "/browse/") {
		return path
	}
	parts := strings.SplitN(strings.TrimLeft(path, "/"), "/", 2)
	slug := parts[0]
	if reservedPaths[slug] {
		return "/browse/new"
	}
	return "/" + slug + "/newest_uploads"
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	pathBase := s.resolveMode(studioURL)
	seen := make(map[string]bool)
	skipped := 0

	// No listing on this site is ordered by publication date. /browse/new is
	// ordered by *upload* date and a repost re-uploads: eight of the first ten
	// cards are reposts of 2025 videos, each carrying "Originally posted on"
	// (which is the date FSS stores). So a stored scene sits at the very top
	// and an early stop would end the run before reaching anything new. The
	// hint is therefore dropped everywhere and known scenes are skipped
	// instead — cheap, since the skip happens before the detail fetch, leaving
	// an incremental run paying for listing pages only.
	paginateOpts := opts
	paginateOpts.KnownIDs = nil

	scraper.Paginate(ctx, paginateOpts, "rawfuckclub", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := pathBase
		if page > 1 {
			u += "?page=" + strconv.Itoa(page)
		}

		body, err := s.fetch(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}

		entries := parseListing(body)

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
			if opts.KnownIDs[e.id] {
				skipped++
				continue
			}
			work = append(work, e)
		}

		results := s.fetchDetails(ctx, work, opts.Delay, studioURL)

		var scenes []models.Scene
		for _, sc := range results {
			if sc.Err != nil {
				return scraper.PageResult{}, sc.Err
			}
			scenes = append(scenes, sc.Scene)
		}
		// A page of nothing but stored scenes is not the end of the listing —
		// on a repost day it is most of page one.
		return scraper.PageResult{Scenes: scenes, Continue: true}, nil
	})

	if skipped > 0 {
		scraper.Debugf(1, "rawfuckclub: skipped %d already-stored scene(s)", skipped)
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
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
			// Breaking here stops launching goroutines, so every remaining slot
			// would keep its zero value — and the consumer reads `Err == nil` as
			// success and emits an empty scene for each. Mark them instead.
			for j := i; j < len(entries); j++ {
				results[j] = sceneOrErr{Err: ctx.Err()}
			}
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, entry listEntry) {
			defer wg.Done()
			defer func() { <-sem }()
			if !scraper.Pace(ctx, delay) {
				// Record the cancellation rather than returning silently.
				// results is index-addressed, so an unwritten slot stays a
				// zero value — and the consumer treats `Err == nil` as
				// success, appending an empty models.Scene with no ID or
				// title as if it were a real scrape result.
				results[idx] = sceneOrErr{Err: ctx.Err()}
				return
			}
			body, err := s.fetch(ctx, entry.url)
			if err != nil {
				results[idx] = sceneOrErr{Err: fmt.Errorf("detail %s: %w", entry.id, err)}
				return
			}
			detail := parseDetail(body)
			results[idx] = sceneOrErr{Scene: buildScene(entry, detail, studioURL, s.base)}
		}(i, e)
	}
	wg.Wait()
	return results
}

func buildScene(e listEntry, d detailData, studioURL string, base string) models.Scene {
	sceneURL := e.url
	if !strings.HasPrefix(sceneURL, "http") {
		sceneURL = base + sceneURL
	}
	sc := models.Scene{
		ID:          e.id,
		SiteID:      "rawfuckclub",
		StudioURL:   studioURL,
		Title:       e.title,
		URL:         sceneURL,
		Thumbnail:   e.thumb,
		Performers:  d.performers,
		Tags:        d.tags,
		Duration:    d.duration,
		Description: d.description,
		ScrapedAt:   time.Now().UTC(),
	}
	if d.studio != "" {
		sc.Studio = d.studio
	} else if e.channel != "" {
		sc.Studio = e.channel
	}
	if d.date != "" {
		if t, err := time.Parse("January 2, 2006", d.date); err == nil {
			sc.Date = t.UTC()
		}
	}
	if d.buyPrice > 0 {
		sc.AddPrice(models.PriceSnapshot{Date: time.Now().UTC(), Regular: d.buyPrice})
	}
	return sc
}

func (s *Scraper) fetch(ctx context.Context, u string) (string, error) {
	if !strings.HasPrefix(u, "http") {
		u = s.base + u
	}
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: u,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Cookie"] = "consent=1"
			return h
		}(),
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
