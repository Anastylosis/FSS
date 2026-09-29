// Package stasyqvr scrapes StasyQVR (stasyqvr.com), the VR arm of the StasyQ
// network. The site is a Laravel app behind an age gate: a scrape first GETs
// /age-confirmation to read a CSRF _token, POSTs it to /age-confirm to flip the
// session cookie, then walks the /virtualreality/list?page={N} listing. Each
// card carries the scene id, title and cover; the /virtualreality/scene/id/{N}
// detail page adds the release date and description. Per-scene performer and
// runtime are not exposed publicly (the model name only appears in the title),
// so those fields are left empty and Studio is set instead.
package stasyqvr

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "stasyqvr"
	studioName = "StasyQVR"
	siteBase   = "https://stasyqvr.com"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?stasyqvr\.com(?:/|$)`)

type Scraper struct {
	// base is the site root used for *fetches*, a field so offline tests can
	// drive the age-confirmation handshake and the listing walk.
	base string
}

func New() *Scraper { return &Scraper{base: siteBase} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"stasyqvr.com",
		"stasyqvr.com/virtualreality/list?page={N}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listingScene struct {
	id    string
	url   string
	title string
	thumb string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	// A fresh cookie-jar client per scrape so concurrent ListScenes calls don't
	// share session state.
	jar, _ := cookiejar.New(nil)
	client := httpx.NewClient(30 * time.Second)
	client.Jar = jar

	if err := s.confirmAge(ctx, client); err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("age confirmation: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listingScene)
	var wg sync.WaitGroup
	scraper.Debugf(1, "stasyqvr: fetching detail pages with %d workers", workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ls := range work {
				scene, err := s.fetchDetail(ctx, client, ls, studioURL, opts.Delay)
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)
		s.enqueueListing(ctx, client, opts, out, work)
	}()

	wg.Wait()
}

var tokenRe = regexp.MustCompile(`name="_token"\s+value="([^"]+)"`)

// confirmAge performs the GET-token → POST-confirm handshake that flips the
// Laravel session cookie so listing pages stop redirecting to /age-confirmation.
func (s *Scraper) confirmAge(ctx context.Context, client *http.Client) error {
	scraper.Debugf(1, "stasyqvr: confirming age gate")
	body, err := fetch(ctx, client, http.MethodGet, s.base+"/age-confirmation", nil)
	if err != nil {
		return err
	}
	m := tokenRe.FindSubmatch(body)
	if m == nil {
		return fmt.Errorf("could not find _token on age-confirmation page")
	}
	form := url.Values{"_token": {string(m[1])}, "confirm": {"1"}}
	_, err = fetch(ctx, client, http.MethodPost, s.base+"/age-confirm", []byte(form.Encode()))
	return err
}

func (s *Scraper) enqueueListing(ctx context.Context, client *http.Client, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "stasyqvr: fetching page %d", page)
		pageURL := fmt.Sprintf("%s/virtualreality/list?page=%d", s.base, page)
		body, err := fetch(ctx, client, http.MethodGet, pageURL, nil)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}
		scenes := parseListing(s.base, body)
		if len(scenes) == 0 {
			return
		}
		for _, ls := range scenes {
			if opts.KnownIDs[ls.id] {
				scraper.Debugf(1, "stasyqvr: hit known ID %s, stopping early", ls.id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- ls:
			case <-ctx.Done():
				return
			}
		}
	}
}

var (
	cardLinkRe = regexp.MustCompile(`href="[^"]*?/virtualreality/scene/id/(\d+)"`)
	cardImgRe  = regexp.MustCompile(`<img src="([^"]+)"[^>]*alt="([^"]*)"`)

	detailH1Re   = regexp.MustCompile(`<h1[^>]*>\s*([^<]+?)\s*</h1>`)
	detailDateRe = regexp.MustCompile(`main-desc__date"[^>]*>\s*([A-Za-z]{3} \d{1,2}, \d{4})\s*<`)
	detailDescRe = regexp.MustCompile(`(?s)<div class="main-desc__detail">.*?<p>(.*?)</p>`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
)

// parseListing extracts the scene id, URL, title and cover from each card.
// Cards repeat the scene link, so the first occurrence per id wins.
func parseListing(base string, body []byte) []listingScene {
	page := string(body)

	// Split into per-card blocks on the cover image, then read the scene link
	// that follows. Simpler: walk every cover img and pair it with the nearest
	// following scene link.
	var scenes []listingScene
	seen := map[string]bool{}
	for _, loc := range cardImgRe.FindAllStringSubmatchIndex(page, -1) {
		imgSrc := page[loc[2]:loc[3]]
		alt := page[loc[4]:loc[5]]
		// Find the scene link after this image.
		rest := page[loc[1]:]
		lm := cardLinkRe.FindStringSubmatch(rest)
		if lm == nil {
			continue
		}
		id := lm[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		scenes = append(scenes, listingScene{
			id:    id,
			url:   base + "/virtualreality/scene/id/" + id,
			title: strings.TrimSpace(html.UnescapeString(alt)),
			thumb: imgSrc,
		})
	}
	return scenes
}

type detailData struct {
	title       string
	date        time.Time
	description string
}

func parseDetail(body []byte) detailData {
	var d detailData
	page := string(body)
	if m := detailH1Re.FindStringSubmatch(page); m != nil {
		d.title = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := detailDateRe.FindStringSubmatch(page); m != nil {
		if t, err := time.Parse("Jan 2, 2006", strings.TrimSpace(m[1])); err == nil {
			d.date = t.UTC()
		}
	}
	if m := detailDescRe.FindStringSubmatch(page); m != nil {
		raw := tagStripRe.ReplaceAllString(m[1], "")
		d.description = strings.TrimSpace(html.UnescapeString(raw))
	}
	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, client *http.Client, ls listingScene, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	scene := models.Scene{
		ID:        ls.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       ls.url,
		Title:     ls.title,
		Thumbnail: ls.thumb,
		Studio:    studioName,
		ScrapedAt: time.Now().UTC(),
	}

	body, err := fetch(ctx, client, http.MethodGet, ls.url, nil)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", ls.id, err)
	}
	d := parseDetail(body)
	if d.title != "" {
		scene.Title = d.title
	}
	scene.Date = d.date
	scene.Description = d.description
	return scene, nil
}

func fetch(ctx context.Context, client *http.Client, method, url string, body []byte) ([]byte, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	if body != nil {
		headers["Content-Type"] = "application/x-www-form-urlencoded"
	}
	resp, err := httpx.Do(ctx, client, httpx.Request{
		Method:  method,
		URL:     url,
		Body:    body,
		Headers: headers,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
