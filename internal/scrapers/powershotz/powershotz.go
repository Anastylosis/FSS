package powershotz

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID         = "powershotz"
	studioName     = "Powershotz"
	defaultSite    = "https://powershotz.com"
	defaultData    = "https://data.powershotz.com"
	defaultWorkers = 2
)

type Scraper struct {
	client   *http.Client
	siteBase string // public site, used for scene URLs and Origin/Referer
	dataBase string // data.powershotz.com, which serves the JSON endpoints
}

func New() *Scraper {
	return &Scraper{
		client:   httpx.NewClient(60 * time.Second),
		siteBase: defaultSite,
		dataBase: defaultData,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"powershotz.com",
		"powershotz.com/videos",
		"powershotz.com/{model}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?powershotz\.com(?:/[^/?#]*)?/?(?:[?#].*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- API types ----

type product struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Price       string `json:"price"`
	Name        string `json:"name"`
	Image       string `json:"image"`
	Length      string `json:"length"`
	Type        string `json:"type"`
	Tags        string `json:"tags"`
}

type star struct {
	ModelName string `json:"model_name"`
	VidID     string `json:"vid_id"`
}

// ---- runner ----

// catalogPaths are the site's own pages that list the whole video catalogue.
var catalogPaths = map[string]bool{"": true, "videos": true}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	model, err := modelFromURL(studioURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}

	clips, err := s.fetchStream(ctx, "clip")
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}

	if model != "" {
		s.runModel(ctx, studioURL, model, clips, opts, out)
		return
	}

	// One request returns the whole catalogue, in no date order, so there
	// is nothing to stop early on. Known clips are skipped instead, which
	// spares their performer lookups.
	scraper.Debugf(1, "%s: catalogue lists %d clips", siteID, len(clips))
	if !send(ctx, out, scraper.Progress(len(clips))) {
		return
	}
	var fresh []product
	for _, c := range clips {
		if !opts.KnownIDs[c.ID] {
			fresh = append(fresh, c)
		}
	}
	skipped := len(clips) - len(fresh)

	var stars map[string][]string
	if len(fresh) > 0 {
		stars = s.performers(ctx, fresh, opts, out)
		if ctx.Err() != nil {
			return
		}
	}
	now := time.Now().UTC()
	for _, c := range fresh {
		if !send(ctx, out, scraper.Scene(s.toScene(studioURL, c, stars[c.ID], now))) {
			return
		}
	}
	if skipped > 0 {
		scraper.Debugf(1, "%s: skipped %d already-stored clips", siteID, skipped)
		send(ctx, out, scraper.StoppedEarly())
	}
}

// runModel scrapes one model's videos. The model is a "photos" product whose
// title is the page path; its video list comes from the stars endpoint.
func (s *Scraper) runModel(ctx context.Context, studioURL, model string, clips []product, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	scraper.Debugf(1, "%s: scraping model page %q", siteID, model)
	sets, err := s.fetchStream(ctx, "photos")
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	var m *product
	for i := range sets {
		if strings.EqualFold(sets[i].Title, model) {
			m = &sets[i]
			break
		}
	}
	if m == nil {
		send(ctx, out, scraper.Error(fmt.Errorf("%s: no model named %q on the site", siteID, model)))
		return
	}
	rows, err := s.fetchStars(ctx, m.ID, m.Title)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	ids := map[string]bool{}
	for _, r := range rows {
		if r.ModelName == m.Title {
			ids[r.VidID] = true
		}
	}
	var mine []product
	for _, c := range clips {
		if ids[c.ID] {
			mine = append(mine, c)
		}
	}
	if !send(ctx, out, scraper.Progress(len(mine))) {
		return
	}
	// Per-clip lookups, so co-stars are credited too.
	stars := s.performersByClip(ctx, mine, opts, out)
	if ctx.Err() != nil {
		return
	}
	now := time.Now().UTC()
	for _, c := range mine {
		if !send(ctx, out, scraper.Scene(s.toScene(studioURL, c, stars[c.ID], now))) {
			return
		}
	}
}

// modelFromURL returns the model name a studio URL filters on, or "" for the
// whole catalogue.
func modelFromURL(studioURL string) (string, error) {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", studioURL, err)
	}
	p := strings.Trim(u.Path, "/")
	if catalogPaths[strings.ToLower(p)] {
		return "", nil
	}
	if clipIDRe.MatchString(p) {
		return "", fmt.Errorf("%s: %q is a single video, not a listing", siteID, studioURL)
	}
	return p, nil
}

var clipIDRe = regexp.MustCompile(`(?i)^(?:\d+|pzv\d+)$`)

// performers maps clip ID to performer names for the given clips. The stars
// endpoint answers per clip or per model; whichever needs fewer requests wins.
func (s *Scraper) performers(ctx context.Context, clips []product, opts scraper.ListOpts, out chan<- scraper.SceneResult) map[string][]string {
	sets, err := s.fetchStream(ctx, "photos")
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return nil
	}
	if len(clips) <= len(sets) {
		return s.performersByClip(ctx, clips, opts, out)
	}
	scraper.Debugf(1, "%s: fetching stars for %d models with %d workers", siteID, len(sets), scraper.WorkerCount(opts, defaultWorkers))
	byClip := map[string][]string{}
	var mu sync.Mutex
	s.pool(ctx, len(sets), opts, func(i int) {
		m := sets[i]
		rows, err := s.fetchStars(ctx, m.ID, m.Title)
		if err != nil {
			send(ctx, out, scraper.Error(err))
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, r := range rows {
			if r.ModelName == m.Title {
				byClip[r.VidID] = append(byClip[r.VidID], r.ModelName)
			}
		}
	})
	for id, names := range byClip {
		// Workers finish in any order; the site lists stars alphabetically.
		slices.Sort(names)
		byClip[id] = normalizePerformers(names)
	}
	return byClip
}

func (s *Scraper) performersByClip(ctx context.Context, clips []product, opts scraper.ListOpts, out chan<- scraper.SceneResult) map[string][]string {
	scraper.Debugf(1, "%s: fetching stars for %d clips with %d workers", siteID, len(clips), scraper.WorkerCount(opts, defaultWorkers))
	byClip := map[string][]string{}
	var mu sync.Mutex
	s.pool(ctx, len(clips), opts, func(i int) {
		c := clips[i]
		rows, err := s.fetchStars(ctx, c.ID, c.ID)
		if err != nil {
			send(ctx, out, scraper.Error(err))
			return
		}
		var names []string
		for _, r := range rows {
			if r.VidID == c.ID {
				names = append(names, r.ModelName)
			}
		}
		slices.Sort(names)
		mu.Lock()
		byClip[c.ID] = normalizePerformers(names)
		mu.Unlock()
	})
	return byClip
}

// pool runs fn for 0..n-1 on a paced worker pool.
func (s *Scraper) pool(ctx context.Context, n int, opts scraper.ListOpts, fn func(int)) {
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range scraper.WorkerCount(opts, defaultWorkers) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				fn(i)
			}
		}()
	}
loop:
	for i := range n {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break loop
		}
	}
	close(jobs)
	wg.Wait()
}

// trailingDigitsRe strips the shoot counter the site appends to a returning
// model ("Ally2" is Ally's second set).
var trailingDigitsRe = regexp.MustCompile(`^(\D+?)\d+$`)

// normalizePerformers splits duo entries ("Aubry & Tatum") and drops the
// shoot counter, de-duplicating while keeping order.
func normalizePerformers(names []string) []string {
	var outNames []string
	seen := map[string]bool{}
	for _, n := range names {
		for _, part := range strings.Split(n, "&") {
			part = strings.TrimSpace(part)
			if m := trailingDigitsRe.FindStringSubmatch(part); m != nil {
				part = strings.TrimSpace(m[1])
			}
			key := strings.ToLower(part)
			if part == "" || seen[key] {
				continue
			}
			seen[key] = true
			outNames = append(outNames, part)
		}
	}
	return outNames
}

// ---- fetching ----

func (s *Scraper) headers() map[string]string {
	h := httpx.XHRHeaders(httpx.UserAgentFirefox, s.siteBase)
	// The data host is a sibling origin, and it 403s a request without a
	// Referer from the site.
	h["Sec-Fetch-Site"] = "same-site"
	h["Referer"] = s.siteBase + "/"
	return h
}

// fetchStream reads one of the NDJSON product streams ("clip" or "photos").
func (s *Scraper) fetchStream(ctx context.Context, kind string) ([]product, error) {
	u := s.dataBase + "/php/stream/gvop.php"
	body, _ := json.Marshal(map[string]string{"st": kind})
	scraper.Debugf(1, "%s: fetching %s stream", siteID, kind)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{Method: http.MethodPost, URL: u, Body: body, Headers: s.headers()})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	items, err := parseStream(data)
	if err != nil {
		return nil, scraper.ParseError(u, err)
	}
	if len(items) == 0 {
		return nil, scraper.ParseError(u, fmt.Errorf("%s stream is empty", kind))
	}
	return items, nil
}

func parseStream(data []byte) ([]product, error) {
	var items []product
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var p product
		if err := json.Unmarshal(line, &p); err != nil {
			return nil, err
		}
		if p.ID == "" || p.Title == "" {
			continue
		}
		items = append(items, p)
	}
	return items, sc.Err()
}

// fetchStars calls the stars endpoint the detail page uses: i is the product
// ID and p the page path (a clip ID, or a model's title).
func (s *Scraper) fetchStars(ctx context.Context, id, path string) ([]star, error) {
	u := s.dataBase + "/php/gsd_p.php"
	body, _ := json.Marshal(map[string]string{"i": id, "p": path})
	var rows []star
	if err := httpx.DoJSON(ctx, s.client, httpx.Request{Method: http.MethodPost, URL: u, Body: body, Headers: s.headers()}, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// ---- scene building ----

func (s *Scraper) toScene(studioURL string, p product, performers []string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          p.ID,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       strings.TrimSpace(p.Title),
		URL:         s.siteBase + "/" + p.ID,
		Description: fixMojibake(strings.TrimSpace(p.Description)),
		Performers:  performers,
		Studio:      studioName,
		Tags:        splitTags(p.Tags, p.Title),
		ScrapedAt:   now,
	}
	if p.Image != "" {
		sc.Thumbnail = s.dataBase + "/c4sImages/" + p.Image
	}
	sc.Preview = s.dataBase + "/previews/prev_" + p.ID + ".mp4"
	if mins, err := strconv.Atoi(strings.TrimSpace(p.Length)); err == nil && mins > 0 {
		sc.Duration = mins * 60
	}
	if i := strings.LastIndex(p.Name, "."); i >= 0 {
		sc.Format = strings.ToLower(p.Name[i+1:])
	}
	if price, err := strconv.ParseFloat(strings.TrimSpace(p.Price), 64); err == nil && price > 0 {
		sc.AddPrice(models.PriceSnapshot{Date: now, Regular: price})
	}
	return sc
}

// splitTags splits the comma-separated tag field, which also repeats the
// clip's own title; that entry is dropped.
func splitTags(raw, title string) []string {
	var tags []string
	seen := map[string]bool{strings.ToLower(strings.TrimSpace(title)): true}
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		key := strings.ToLower(t)
		if t == "" || seen[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, t)
	}
	return tags
}

// fixMojibake repairs UTF-8 text that was decoded as Windows-1252 and
// re-encoded ("youâ€™ve"), which a few descriptions carry.
func fixMojibake(s string) string {
	if !strings.Contains(s, "â€") {
		return s
	}
	r := strings.NewReplacer(
		"â€™", "’", "â€˜", "‘", "â€œ", "“", "â€\u009d", "”",
		"â€¦", "…", "â€“", "–", "â€”", "—",
	)
	return r.Replace(s)
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}
