package maturenl

import (
	"context"
	"errors"
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

// siteBase is a var (not const) so tests can point it at a local httptest server.
var siteBase = "https://www.mature.nl"

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "maturenl" }

func (s *Scraper) Patterns() []string {
	return []string{
		"mature.nl/en/updates",
		"mature.nl/en/model/{id}",
		"mature.nl/en/niche/{id}/{page}/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?mature\.nl/en/(updates|model/\d+|niche/\d+)`)

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

type urlKind int

const (
	kindUpdates urlKind = iota
	kindModel
	kindNiche
)

var (
	modelURLRe = regexp.MustCompile(`/en/model/(\d+)`)
	nicheURLRe = regexp.MustCompile(`/en/niche/(\d+)`)
)

func classifyURL(u string) (urlKind, string) {
	if m := modelURLRe.FindStringSubmatch(u); m != nil {
		return kindModel, m[1]
	}
	if m := nicheURLRe.FindStringSubmatch(u); m != nil {
		return kindNiche, m[1]
	}
	return kindUpdates, ""
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	kind, id := classifyURL(studioURL)

	switch kind {
	case kindModel:
		s.runModel(ctx, studioURL, id, opts, out)
	case kindNiche:
		s.runPaginated(ctx, studioURL, opts, out, func(page int) string {
			return fmt.Sprintf("%s/en/niche/%s/%d", siteBase, id, page)
		})
	default:
		s.runPaginated(ctx, studioURL, opts, out, func(page int) string {
			return fmt.Sprintf("%s/en/updates/%d", siteBase, page)
		})
	}
}

func (s *Scraper) runPaginated(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, pageURL func(int) string) {
	now := time.Now().UTC()

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return
			}
		}
		scraper.Debugf(1, "maturenl: fetching page %d", page)

		body, err := s.fetch(ctx, pageURL(page))
		if err != nil {
			// Past the last page the listing 404s (live-verified: /en/updates/162
			// is the last, 163 is a 404), so every full run reported a failure
			// and was demoted to non-authoritative. Only past page 1 — a 404 on
			// page 1 is a bad niche or model slug and must stay loud.
			if page > 1 && isNotFound(err) {
				scraper.Debugf(1, "maturenl: page %d is past the last page (404) — done", page)
				return
			}
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		cards := parseListingCards(body)
		if len(cards) == 0 {
			return
		}

		if page == 1 {
			total := estimateTotal(body, len(cards))
			if total > 0 {
				scraper.Debugf(1, "maturenl: %d total scenes", total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
		}

		for _, c := range cards {
			if opts.KnownIDs[c.id] {
				scraper.Debugf(1, "maturenl: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}

			var scene models.Scene
			if c.title == "" {
				scraper.Debugf(1, "maturenl: card %s has no title, fetching detail page", c.id)
				var err error
				scene, err = s.fetchDetailScene(ctx, studioURL, c.id)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
					}
					continue
				}
			} else {
				scene = cardToScene(c, studioURL, now)
			}
			select {
			case out <- scraper.Scene(scene):
			case <-ctx.Done():
				return
			}
		}
	}
}

// runModel extracts update URLs from a model page (sparse cards), then
// fetches each detail page via a worker pool.
func (s *Scraper) runModel(ctx context.Context, studioURL string, modelID string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetch(ctx, fmt.Sprintf("%s/en/model/%s", siteBase, modelID))
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	ids := parseModelUpdateIDs(body)
	if len(ids) == 0 {
		return
	}

	select {
	case out <- scraper.Progress(len(ids)):
	case <-ctx.Done():
		return
	}

	work := make(chan string, opts.Workers)
	var wg sync.WaitGroup

	// stopped is closed by the first worker that hits a known ID. The producer
	// loop below selects on it so it stops enqueuing once the workers have
	// early-stopped — otherwise it blocks forever on a full work buffer that no
	// worker is draining (ctx is not cancelled on a normal early-stop), hanging
	// the scrape while it holds the store flock. See AUDIT_PLAN B1b.
	stopped := make(chan struct{})
	var stopOnce sync.Once
	signalStop := func() { stopOnce.Do(func() { close(stopped) }) }

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for updateID := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}

				if opts.KnownIDs[updateID] {
					scraper.Debugf(1, "maturenl: hit known ID, stopping early")
					select {
					case out <- scraper.StoppedEarly():
					case <-ctx.Done():
					}
					signalStop()
					return
				}

				scene, ferr := s.fetchDetailScene(ctx, studioURL, updateID)
				if ferr != nil {
					select {
					case out <- scraper.Error(ferr):
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

enqueue:
	for _, uid := range ids {
		select {
		case work <- uid:
		case <-stopped:
			break enqueue
		case <-ctx.Done():
			break enqueue
		}
	}

	close(work)
	wg.Wait()
}

// isNotFound reports whether err is the listing answering 404.
func isNotFound(err error) bool {
	var se *httpx.StatusError
	return errors.As(err, &se) && se.StatusCode == http.StatusNotFound
}

func (s *Scraper) fetch(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func (s *Scraper) fetchDetailScene(ctx context.Context, studioURL string, updateID string) (models.Scene, error) {
	now := time.Now().UTC()
	url := fmt.Sprintf("%s/en/update/%s", siteBase, updateID)

	body, err := s.fetch(ctx, url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("update %s: %w", updateID, err)
	}

	d := parseDetailPage(body)

	scene := models.Scene{
		ID:          updateID,
		SiteID:      "maturenl",
		StudioURL:   studioURL,
		Title:       d.title,
		URL:         url,
		Thumbnail:   d.thumbnail,
		Preview:     d.preview,
		Description: d.description,
		Performers:  d.performers,
		Tags:        d.tags,
		Duration:    d.duration,
		Date:        d.date,
		Studio:      d.producer,
		ScrapedAt:   now,
	}
	scene.AddPrice(models.PriceSnapshot{Date: now, IsFree: false})
	return scene, nil
}

// --- Listing card parsing (niche/updates pages) ---

type card struct {
	id         string
	title      string
	url        string
	thumbnail  string
	performers []string
	tags       []string
	producer   string
	date       time.Time
}

var (
	gridItemRe  = regexp.MustCompile(`(?s)<div class="grid-item">`)
	updateIDRe  = regexp.MustCompile(`/en/update/(\d+)`)
	cardTitleRe = regexp.MustCompile(`(?s)card-title">\s*<a[^>]*>([^<]+)</a>`)
	thumbRe     = regexp.MustCompile(`data-src="(https?://[^"]*?/cs_en\.jpg[^"]*)"`)
	subtitleRe  = regexp.MustCompile(`(?s)card-subtitle">(.*?)</div>`)
	perfLinkRe  = regexp.MustCompile(`<a\s+href="/en/model/\d+[^"]*">([^<]+)</a>`)
	tagAreaRe   = regexp.MustCompile(`(?s)card-text">\s*<div class="overflow">(.*?)</div>`)
	tagLinkRe   = regexp.MustCompile(`<a\s+href="/en/niche/[^"]*">([^<]+)</a>`)
	metaAreaRe  = regexp.MustCompile(`(?s)card-text fs-small">\s*<div class="overflow">(.*?)</div>`)
	dateRe      = regexp.MustCompile(`(\d{1,2}-\d{1,2}-\d{4})`)
	pageNavRe   = regexp.MustCompile(`(?s)<div class="page-nav">(.*?)</div>`)
	lastPageRe  = regexp.MustCompile(`href="[^"]*?/(\d+)[^"]*"[^>]*>\s*<span[^>]*>&#xE5DD;`)
	stripTagsRe = regexp.MustCompile(`<[^>]+>`)
)

func parseListingCards(body []byte) []card {
	locs := gridItemRe.FindAllIndex(body, -1)
	cards := make([]card, 0, len(locs))

	for i, loc := range locs {
		start := loc[0]
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		content := body[start:end]
		c := card{}

		if m := updateIDRe.FindSubmatch(content); m != nil {
			c.id = string(m[1])
			c.url = siteBase + "/en/update/" + c.id
		}
		if c.id == "" {
			continue
		}

		if m := cardTitleRe.FindSubmatch(content); m != nil {
			c.title = strings.TrimSpace(html.UnescapeString(string(m[1])))
		}
		if m := thumbRe.FindSubmatch(content); m != nil {
			c.thumbnail = string(m[1])
		}

		if m := subtitleRe.FindSubmatch(content); m != nil {
			for _, pm := range perfLinkRe.FindAllSubmatch(m[1], -1) {
				name := strings.TrimSpace(html.UnescapeString(string(pm[1])))
				if name != "" {
					c.performers = append(c.performers, name)
				}
			}
		}

		tagAreas := tagAreaRe.FindAllSubmatch(content, -1)
		if len(tagAreas) > 0 {
			for _, tm := range tagLinkRe.FindAllSubmatch(tagAreas[0][1], -1) {
				tag := strings.TrimSpace(html.UnescapeString(string(tm[1])))
				if tag != "" {
					c.tags = append(c.tags, tag)
				}
			}
		}

		if m := metaAreaRe.FindSubmatch(content); m != nil {
			meta := stripTagsRe.ReplaceAllString(string(m[1]), "")
			meta = html.UnescapeString(meta)
			meta = strings.TrimSpace(meta)

			if parts := strings.SplitN(meta, "•", 2); len(parts) == 2 {
				c.producer = strings.TrimSpace(parts[0])
				c.date = parseDate(strings.TrimSpace(parts[1]))
			} else if dm := dateRe.FindString(meta); dm != "" {
				c.date = parseDate(dm)
				c.producer = strings.TrimSpace(strings.Replace(meta, dm, "", 1))
				c.producer = strings.TrimRight(c.producer, " •")
			}
		}

		cards = append(cards, c)
	}

	return cards
}

func estimateTotal(body []byte, firstPageCount int) int {
	nav := pageNavRe.FindSubmatch(body)
	if nav == nil {
		return firstPageCount
	}
	if m := lastPageRe.FindSubmatch(nav[1]); m != nil {
		lastPage, _ := strconv.Atoi(string(m[1]))
		if lastPage > 0 {
			return lastPage * firstPageCount
		}
	}
	return firstPageCount
}

func cardToScene(c card, studioURL string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         c.id,
		SiteID:     "maturenl",
		StudioURL:  studioURL,
		Title:      c.title,
		URL:        c.url,
		Thumbnail:  c.thumbnail,
		Performers: c.performers,
		Tags:       c.tags,
		Date:       c.date,
		Studio:     c.producer,
		ScrapedAt:  now,
	}
	scene.AddPrice(models.PriceSnapshot{Date: now, IsFree: false})
	return scene
}

// --- Detail page parsing (for model URL mode) ---

type detailPage struct {
	title       string
	thumbnail   string
	preview     string
	description string
	performers  []string
	tags        []string
	producer    string
	date        time.Time
	duration    int
}

var (
	detailTitleRe   = regexp.MustCompile(`<h1[^>]*>([^<]+)</h1>`)
	detailThumbRe   = regexp.MustCompile(`poster="(https?://[^"]*?/cs_wide\.jpg[^"]*)"`)
	detailTrailerRe = regexp.MustCompile(`<source\s+src="(https?://[^"]*?trailer[^"]*\.mp4[^"]*)"`)
	detailDateRe    = regexp.MustCompile(`title="Release date">[^<]*</span>\s*<span class="val-m">([^<]+)</span>`)
	detailDurRe     = regexp.MustCompile(`title="Video length">[^<]*</span>\s*<span class="val-m">([^<]+)</span>`)
	detailStarRe    = regexp.MustCompile(`(?s)col-accent">Starring:</span>\s*(.+?)(?:<br|</div)`)
	detailSynRe     = regexp.MustCompile(`(?s)col-accent">Synopsis:</span>\s*(.+?)(?:</div|<br)`)
	detailTagRe     = regexp.MustCompile(`<a[^>]*class="tag"[^>]*>([^<]+)</a>`)
	detailProdRe    = regexp.MustCompile(`(?s)col-accent">Producer:</span>\s*([^<]+)`)
	ageParenRe      = regexp.MustCompile(`\s*\(\d+\)\s*$`)
)

func parseDetailPage(body []byte) detailPage {
	d := detailPage{}

	if m := detailTitleRe.FindSubmatch(body); m != nil {
		d.title = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}
	if m := detailThumbRe.FindSubmatch(body); m != nil {
		d.thumbnail = string(m[1])
	}
	if m := detailTrailerRe.FindSubmatch(body); m != nil {
		d.preview = string(m[1])
	}
	if m := detailDateRe.FindSubmatch(body); m != nil {
		d.date = parseDate(strings.TrimSpace(string(m[1])))
	}
	if m := detailDurRe.FindSubmatch(body); m != nil {
		d.duration = parseutil.ParseDurationColon(strings.TrimSpace(string(m[1])))
	}
	if m := detailStarRe.FindSubmatch(body); m != nil {
		raw := stripTagsRe.ReplaceAllString(string(m[1]), "")
		raw = html.UnescapeString(raw)
		raw = strings.ReplaceAll(raw, "&", ",")
		for _, p := range strings.Split(raw, ",") {
			name := strings.TrimSpace(p)
			name = ageParenRe.ReplaceAllString(name, "")
			if name != "" {
				d.performers = append(d.performers, name)
			}
		}
	}
	if m := detailSynRe.FindSubmatch(body); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}
	for _, m := range detailTagRe.FindAllSubmatch(body, -1) {
		tag := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if tag != "" {
			d.tags = append(d.tags, tag)
		}
	}
	if m := detailProdRe.FindSubmatch(body); m != nil {
		d.producer = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}

	return d
}

// --- Model page parsing ---

var modelUpdateRe = regexp.MustCompile(`/en/update/(\d+)`)

func parseModelUpdateIDs(body []byte) []string {
	matches := modelUpdateRe.FindAllSubmatch(body, -1)
	seen := make(map[string]bool, len(matches))
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		id := string(m[1])
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// --- Helpers ---

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	t, err := time.Parse("2-1-2006", s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
