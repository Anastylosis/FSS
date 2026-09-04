package ladyfyre

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

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "ladyfyre" }

func (s *Scraper) Patterns() []string {
	return []string{
		"ladyfyre.com",
		"ladyfyre.com/tour/models/{slug}.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?ladyfyre\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

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
	return "https://www.ladyfyre.com"
}

type listEntry struct {
	slug       string
	url        string
	title      string
	performers []string
	date       string
	thumbnail  string
	price      float64
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	isModel := strings.Contains(studioURL, "/models/")
	if isModel {
		s.runSinglePage(ctx, s.resolveURL(studioURL), opts, out)
		return
	}

	s.runPaginated(ctx, opts, out)
}

func (s *Scraper) runSinglePage(ctx context.Context, modelURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetchHTML(ctx, modelURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	entries := parseModelEntries(body)
	if len(entries) == 0 {
		// Surfaced rather than returning silently. A silent zero here is what
		// hid this mode being broken: the page fetched fine, no cards parsed,
		// and the scrape reported success with nothing in it. A model with
		// genuinely no videos also lands here, which is still worth telling the
		// operator about.
		select {
		case out <- scraper.Error(fmt.Errorf("no update cards parsed from model page %s", modelURL)):
		case <-ctx.Done():
		}
		return
	}

	select {
	case out <- scraper.Progress(len(entries)):
	case <-ctx.Done():
		return
	}

	s.processEntries(ctx, entries, opts, out)
}

func (s *Scraper) runPaginated(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	work := make(chan listEntry, opts.Workers)
	var wg sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
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
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
			}
			if ctx.Err() != nil {
				break
			}
		}
		scraper.Debugf(1, "ladyfyre: fetching page %d", page)

		pageURL := fmt.Sprintf("%s/tour/categories/movies_%d_d.html", s.base(), page)
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

		if !sentTotal {
			sentTotal = true
			maxPage := parseMaxPage(body)
			if maxPage > 0 {
				select {
				case out <- scraper.Progress(maxPage * len(entries)):
				case <-ctx.Done():
				}
			}
		}

		cancelled := false
		hitKnown := false
		for _, e := range entries {
			if opts.KnownIDs[e.slug] {
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
				scraper.Debugf(1, "ladyfyre: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
			}
			break
		}

		if !hasNextPage(body, page) {
			break
		}
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) processEntries(ctx context.Context, entries []listEntry, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	work := make(chan listEntry, opts.Workers)
	var wg sync.WaitGroup
	// LIFO: close(work) fires first so workers' `for ... range work` exits,
	// then wg.Wait() blocks until they're all gone. Guarantees no leak even
	// when the entry-feed loop below bails on ctx.Done.
	defer wg.Wait()
	defer close(work)
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
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

	for _, e := range entries {
		if opts.KnownIDs[e.slug] {
			scraper.Debugf(1, "ladyfyre: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			break
		}
		select {
		case work <- e:
		case <-ctx.Done():
			return
		}
	}
}

var (
	linkRe      = regexp.MustCompile(`href="([^"]*?/tour/updates/([^"]+)\.html)"`)
	titleRe     = regexp.MustCompile(`(?s)<h4>\s*<a[^>]*>\s*(.*?)\s*</a>\s*</h4>`)
	performerRe = regexp.MustCompile(`<a href="[^"]*?/tour/models/[^"]*">([^<]+)</a>`)
	dateRe      = regexp.MustCompile(`<span>(\d{2}/\d{2}/\d{4})</span>`)
	thumbRe     = regexp.MustCompile(`class="stdimage[^"]*"\s+src="([^"]+)"`)
	priceRe     = regexp.MustCompile(`Buy\s+\$([0-9.]+)`)
	maxPageRe   = regexp.MustCompile(`movies_(\d+)_d\.html`)

	// Model-page variants of the two patterns the templates do not share.
	modelTitleRe = regexp.MustCompile(`(?s)<span class="update_title">\s*(.*?)\s*</span>`)
	modelDateRe  = regexp.MustCompile(`<span class="availdate">\s*(\d{2}/\d{2}/\d{4})`)
)

// parseListingEntries parses the paginated main listing, whose cards are
// `<div class="updateItem">`.
func parseListingEntries(body []byte) []listEntry {
	return parseEntries(string(body), `<div class="updateItem">`, titleRe, dateRe)
}

// parseModelEntries parses a /tour/models/{slug}.html page.
//
// Model pages run a different template from the main listing: cards are
// `update_block` rather than `updateItem`, the title is a
// `<span class="update_title">` rather than an `<h4><a>`, and the date sits in
// `<span class="availdate">` rather than a bare `<span>`. Only the link,
// performer, thumbnail and price patterns are shared.
//
// Splitting a model page on the listing's delimiter yields zero cards, so
// runSinglePage returned without emitting anything: the /models/ mode was
// advertised in Patterns() but had never produced a scene.
func parseModelEntries(body []byte) []listEntry {
	return parseEntries(string(body), `<div class="update_block">`, modelTitleRe, modelDateRe)
}

func parseEntries(page, delim string, titleRe, dateRe *regexp.Regexp) []listEntry {
	var entries []listEntry
	seen := make(map[string]bool)

	parts := strings.Split(page, delim)
	for _, part := range parts[1:] {
		m := linkRe.FindStringSubmatch(part)
		if m == nil {
			continue
		}
		slug := m[2]
		if seen[slug] {
			continue
		}
		seen[slug] = true

		e := listEntry{
			slug: slug,
			url:  m[1],
		}

		if tm := titleRe.FindStringSubmatch(part); tm != nil {
			e.title = strings.TrimSpace(html.UnescapeString(tm[1]))
		}

		for _, pm := range performerRe.FindAllStringSubmatch(part, -1) {
			name := strings.TrimSpace(html.UnescapeString(pm[1]))
			if name != "" {
				e.performers = append(e.performers, name)
			}
		}

		if dm := dateRe.FindStringSubmatch(part); dm != nil {
			e.date = dm[1]
		}

		if thm := thumbRe.FindStringSubmatch(part); thm != nil {
			e.thumbnail = html.UnescapeString(thm[1])
		}

		if pm := priceRe.FindStringSubmatch(part); pm != nil {
			e.price, _ = strconv.ParseFloat(pm[1], 64)
		}

		entries = append(entries, e)
	}

	return entries
}

func parseMaxPage(body []byte) int {
	maxPage := 0
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

func hasNextPage(body []byte, current int) bool {
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > current {
			return true
		}
	}
	return false
}

var (
	updateTags = regexp.MustCompile(`(?s)<span class="update_tags">\s*(?:Tags:)?\s*(.*?)</span>`)
	tagLinkRe  = regexp.MustCompile(`<a href="[^"]*?/tour/categories/[^"]*">([^<]+)</a>`)
)

func (s *Scraper) resolveURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "http") {
		return rawURL
	}
	return s.base() + rawURL
}

func (s *Scraper) fetchDetail(ctx context.Context, entry listEntry) (models.Scene, error) {
	referer := s.base() + "/tour/categories/movies.html"
	body, err := s.fetchHTMLWithReferer(ctx, s.resolveURL(entry.url), referer)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", entry.slug, err)
	}

	// The listing links are site-relative. Storing them verbatim left every
	// scene with a URL that resolves against nothing outside the scraper.
	return parseDetail(body, entry, s.resolveURL(entry.url)), nil
}

func parseDetail(body []byte, entry listEntry, sceneURL string) models.Scene {
	now := time.Now().UTC()
	scene := models.Scene{
		ID:         entry.slug,
		SiteID:     "ladyfyre",
		StudioURL:  "https://www.ladyfyre.com",
		Title:      entry.title,
		URL:        sceneURL,
		Thumbnail:  entry.thumbnail,
		Performers: entry.performers,
		Studio:     "Lady Fyre",
		ScrapedAt:  now,
	}

	if entry.date != "" {
		if t, err := time.Parse("01/02/2006", entry.date); err == nil {
			scene.Date = t.UTC()
		}
	}

	if entry.price > 0 {
		scene.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: entry.price,
		})
	}

	if v := parseutil.OpenGraph(body)["og:description"]; v != "" {
		scene.Description = strings.TrimSpace(html.UnescapeString(v))
	}

	if m := updateTags.FindSubmatch(body); m != nil {
		for _, tm := range tagLinkRe.FindAllSubmatch(m[1], -1) {
			tag := strings.TrimSpace(html.UnescapeString(string(tm[1])))
			if tag != "" {
				scene.Tags = append(scene.Tags, tag)
			}
		}
	}

	return scene
}

func (s *Scraper) fetchHTML(ctx context.Context, rawURL string) ([]byte, error) {
	return s.fetchHTMLWithReferer(ctx, rawURL, "")
}

func (s *Scraper) fetchHTMLWithReferer(ctx context.Context, rawURL, referer string) ([]byte, error) {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	if referer != "" {
		h["Referer"] = referer
		h["Sec-Fetch-Site"] = "same-origin"
	}
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: h,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
