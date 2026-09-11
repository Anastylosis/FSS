// Package vintageflash scrapes Vintage Flash (vintageflash.com), a hand-rolled
// PHP tour on the Nylons Cash network.
//
// The site exposes no usable listing: the homepage shows ten scenes, `?page=N`
// is ignored, and /models.php only renders an A–Z letter nav with no model
// links. What it does expose is a stable per-scene URL whose id is a
// base64-encoded integer:
//
//	/{slug}_{base64(id)}.html
//
// The slug is ignored by the server, so scenes are enumerated by walking ids
// from 1 upward. Deleted or never-used ids answer HTTP 500, which is treated as
// a gap rather than an error; the walk stops after a run of consecutive gaps.
//
// Every set carries both stills and a video ("161 images and 12:31 video"), so
// there is no photo/video split to filter.
//
// Defunct since 2026-09: the domain 301s to nhlpcentral.com, which carries the
// same sets under its own unrelated ids (see the nhlpcentral scraper). A scrape
// checks the homepage first and reports that as an error instead of walking
// ids that all miss.
package vintageflash

import (
	"context"
	"encoding/base64"
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
	siteID     = "vintageflash"
	studioName = "Vintage Flash"
	// workers bounds concurrent id probes.
	workers = 4
	// maxGap is how many consecutive missing ids end the walk. Gaps inside the
	// catalogue are common (deleted sets), so this is well above the largest
	// run observed.
	maxGap = 60
	// batchSize is how many ids are probed per round.
	batchSize = 24
	// maxID is a hard backstop so a server that stops returning 500s cannot
	// make the walk unbounded. The live catalogue tops out near 1675.
	maxID = 3000
)

var siteBase = "https://vintageflash.com"

// Scraper implements scraper.StudioScraper for Vintage Flash.
type Scraper struct {
	Client *http.Client
}

// New constructs a Vintage Flash scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"vintageflash.com",
		"vintageflash.com/{slug}_{base64id}.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?vintageflash\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// sceneURL builds the page URL for a numeric id. The slug is ignored by the
// server, so a placeholder is used.
func sceneURL(id int) string {
	enc := base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(id)))
	return fmt.Sprintf("%s/set_%s.html", siteBase, enc)
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if err := s.checkHome(ctx); err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	now := time.Now().UTC()

	// Ids are walked in fixed batches: each batch is probed concurrently, then
	// its results are emitted in id order. A batch is cheap to reason about and
	// bounds how far past the end of the catalogue the walk can run.
	gap := 0
	for start := 1; start <= maxID; start += batchSize {
		if ctx.Err() != nil {
			return
		}

		end := start + batchSize
		if end > maxID+1 {
			end = maxID + 1
		}
		scenes, found := s.fetchBatch(ctx, studioURL, start, end, opts.Delay, now)

		for i, sc := range scenes {
			if !found[i] {
				gap++
				continue
			}
			gap = 0
			select {
			case out <- scraper.Scene(sc):
			case <-ctx.Done():
				return
			}
		}

		if gap >= maxGap {
			scraper.Debugf(1, "%s: %d consecutive missing ids by %d, stopping", siteID, gap, end-1)
			return
		}
	}
}

// fetchBatch probes ids in [start, end) concurrently and returns the results in
// id order, alongside a parallel slice marking which ids existed.
func (s *Scraper) fetchBatch(ctx context.Context, studioURL string, start, end int, delay time.Duration, now time.Time) ([]models.Scene, []bool) {
	n := end - start
	scenes := make([]models.Scene, n)
	found := make([]bool, n)

	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}
			scenes[i], found[i] = s.fetchScene(ctx, studioURL, start+i, now)
		}(i)
	}
	wg.Wait()

	return scenes, found
}

// ---- detail ----

var (
	// "Vintage Flash: Chloe Toy - A Vintage Classic"
	titleRe = regexp.MustCompile(`<title>\s*Vintage Flash:\s*([^<]*?)\s*</title>`)
	descRe  = regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
	// "161 images and 12:31 video"
	durationRe = regexp.MustCompile(`images and\s*<span[^>]*>(\d{1,2}:\d{2}(?::\d{2})?)</span>\s*video`)
	dateRe     = regexp.MustCompile(`\b(\d{1,2}(?:st|nd|rd|th)?\s+[A-Z][a-z]+\s+\d{4})\b`)
	thumbRe    = regexp.MustCompile(`(https?://[^"'\s]*/awizicon/\d+_\d+\.jpg)`)
)

// fetchScene probes one id. A missing id answers HTTP 500, which httpx surfaces
// as an error — that is a gap, not a failure, so it is reported as not-found
// rather than propagated.
func (s *Scraper) fetchScene(ctx context.Context, studioURL string, id int, now time.Time) (models.Scene, bool) {
	pageURL := sceneURL(id)
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return models.Scene{}, false
	}
	detail := string(body)

	m := titleRe.FindStringSubmatch(detail)
	if m == nil {
		return models.Scene{}, false
	}

	scene := models.Scene{
		ID:        strconv.Itoa(id),
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       pageURL,
		Studio:    studioName,
		ScrapedAt: now,
	}

	// The title tag is "Model - Scene Title"; split on the first dash.
	full := html.UnescapeString(strings.TrimSpace(m[1]))
	if performer, title, ok := strings.Cut(full, " - "); ok {
		scene.Performers = []string{strings.TrimSpace(performer)}
		scene.Title = strings.TrimSpace(title)
	} else {
		scene.Title = full
	}

	if d := descRe.FindStringSubmatch(detail); d != nil {
		scene.Description = strings.Join(strings.Fields(html.UnescapeString(d[1])), " ")
	}
	if du := durationRe.FindStringSubmatch(detail); du != nil {
		scene.Duration = parseutil.ParseDurationColon(du[1])
	}
	if dt := dateRe.FindStringSubmatch(detail); dt != nil {
		cleaned := parseutil.StripOrdinalSuffix(strings.TrimSpace(dt[1]))
		if t, err := parseutil.TryParseDate(cleaned, "2 January 2006", "2 Jan 2006"); err == nil {
			scene.Date = t.UTC()
		}
	}
	if th := thumbRe.FindStringSubmatch(detail); th != nil {
		scene.Thumbnail = th[1]
	}

	return scene, true
}

// ---- HTTP ----

var errFoldedIntoNHLP = errors.New("vintageflash.com now redirects to nhlpcentral.com, " +
	"which carries the Vintage Flash sets under different ids; scrape https://nhlpcentral.com instead " +
	"(the nhlpcentral scraper) — existing vintageflash scenes are kept but can no longer be refreshed")

// checkHome requests the homepage without following redirects. Missing ids
// on a redirected domain would all look like catalogue gaps, so without this
// the walk would end with zero scenes and no error.
func (s *Scraper) checkHome(ctx context.Context) error {
	c := *s.Client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpx.DoWithStatus(ctx, &c, httpx.Request{
		URL:     siteBase + "/",
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return homeStatusErr(resp)
}

func homeStatusErr(resp *http.Response) error {
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		loc, err := resp.Location()
		if err != nil {
			return fmt.Errorf("%s: HTTP %d redirect without a usable Location", siteBase, resp.StatusCode)
		}
		if isNHLP(loc) {
			return errFoldedIntoNHLP
		}
		return fmt.Errorf("%s: redirects to %s, not the Vintage Flash tour", siteBase, loc)
	case resp.StatusCode != http.StatusOK:
		return &httpx.StatusError{StatusCode: resp.StatusCode}
	}
	return nil
}

func isNHLP(u *url.URL) bool {
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") == "nhlpcentral.com"
}

// fetchPage uses DoWithStatus rather than Do because a missing id is answered
// with HTTP 500. Do would treat that as retryable and spend ~6s of backoff on
// every gap — with hundreds of gaps in the id space that turns a full crawl
// into hours of pointless retries against the site. DoWithStatus passes the
// status straight through and does not retry it.
func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.DoWithStatus(ctx, s.Client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", rawURL, resp.StatusCode)
	}
	return httpx.ReadBody(resp.Body)
}
