// Package bludreams scrapes bludreamsxxx.com, Jewelz Blu's own site, which
// runs the classic Elevated X members template under an /access/ prefix.
//
// Listing card:
//
//	<div class="update_details" data-setid="350">
//	  <a href=".../access/scenes/Galactic-JOI_vids.html">
//	    <video class="update_thumb thumbs stdvideo" poster_4x="/access/content//contentthumbs/11/68/1168-4x.jpg"
//	           src="/access/content//contentthumbs/11/68/1168.mp4"> … </video>
//	  </a>
//	  <!-- Title --> <a href="…_vids.html">Galactic JOI</a>
//	  <span class="update_models">Jewelz Blu</span>
//	  <div class="update_counts">7&nbsp;min&nbsp;of video</div>
//	  <div class="cell update_date"><!-- Date --> 06/21/2026</div>
//	</div>
//
// The card carries everything but the description and tags, which the detail
// page holds in <span class="update_description"> and <span class="update_tags">.
package bludreams

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

const (
	siteID     = "bludreams"
	studioName = "Jewelz Blu"
	defaultURL = "https://bludreamsxxx.com"
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   defaultURL,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"bludreamsxxx.com",
		"bludreamsxxx.com/access/categories/{category}.html",
		"bludreamsxxx.com/access/models/{Model}.html",
	}
}

var (
	matchRe    = regexp.MustCompile(`(?i)^https?://(?:www\.)?bludreamsxxx\.com(?:/|$)`)
	categoryRe = regexp.MustCompile(`/categories/([^/?#]+?)(?:_\d+_[a-z])?\.html`)
	modelRe    = regexp.MustCompile(`/models/([^/?#]+)\.html`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

type urlKind int

const (
	kindUpdates urlKind = iota
	kindCategory
	kindModel
)

// classifyURL decides which listing a studio URL selects. "movies" is the
// catalogue-wide category the site itself links as "Movies", so it is treated
// as the full listing rather than as a filter.
func classifyURL(u string) (urlKind, string) {
	if m := modelRe.FindStringSubmatch(u); m != nil && !strings.EqualFold(m[1], "models") {
		return kindModel, m[1]
	}
	if m := categoryRe.FindStringSubmatch(u); m != nil && !strings.EqualFold(m[1], "movies") {
		return kindCategory, m[1]
	}
	return kindUpdates, ""
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	kind, slug := classifyURL(studioURL)
	switch kind {
	case kindModel:
		scraper.Debugf(1, "%s: scraping model page %q", siteID, slug)
	case kindCategory:
		scraper.Debugf(1, "%s: scraping category %q", siteID, slug)
	default:
		scraper.Debugf(1, "%s: scraping full catalogue", siteID)
	}

	items, sentTotal, ok := s.collectListing(ctx, studioURL, kind, slug, opts, out)
	if !ok || ctx.Err() != nil {
		return
	}
	if !sentTotal && len(items) > 0 {
		select {
		case out <- scraper.Progress(len(items)):
		case <-ctx.Done():
			return
		}
	}
	s.fetchDetails(ctx, studioURL, items, opts, out)
}

// listingURL builds the page URL for a mode. Model pages paginate through
// sets.php with the numeric model id the first page discloses; everything else
// is a `_{page}_d.html` (date-sorted) category file.
func (s *Scraper) listingURL(studioURL string, kind urlKind, slug string, page int, modelID string) string {
	switch kind {
	case kindModel:
		if page == 1 {
			return studioURL
		}
		return fmt.Sprintf("%s/access/sets.php?id=%s&page=%d", s.base, modelID, page)
	case kindCategory:
		return fmt.Sprintf("%s/access/categories/%s_%d_d.html", s.base, slug, page)
	default:
		return fmt.Sprintf("%s/access/categories/movies_%d_d.html", s.base, page)
	}
}

func (s *Scraper) collectListing(ctx context.Context, studioURL string, kind urlKind, slug string, opts scraper.ListOpts, out chan<- scraper.SceneResult) (items []listItem, sentTotal, ok bool) {
	seen := make(map[string]bool)
	modelID := ""

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return items, sentTotal, false
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return items, sentTotal, false
			}
		}
		if kind == kindModel && page > 1 && modelID == "" {
			return items, sentTotal, true
		}

		pageURL := s.listingURL(studioURL, kind, slug, page, modelID)
		scraper.Debugf(1, "%s: fetching listing page %d (%s)", siteID, page, pageURL)

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return items, sentTotal, false
		}

		parsed := parseListing(body)
		if len(parsed) == 0 {
			return items, sentTotal, true
		}

		if page == 1 {
			modelID = extractModelID(body)
			total := estimateTotal(body, len(parsed))
			if total > 0 {
				scraper.Debugf(1, "%s: ~%d scenes", siteID, total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return items, sentTotal, false
				}
				sentTotal = true
			}
		}

		for _, item := range parsed {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			if opts.KnownIDs[item.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, item.id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return items, sentTotal, true
			}
			items = append(items, item)
		}

		if !hasNextPage(body, page) {
			return items, sentTotal, true
		}
	}
}

func (s *Scraper) fetchDetails(ctx context.Context, studioURL string, items []listItem, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)

	work := make(chan listItem, workers)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer close(work)

	now := time.Now().UTC()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				body, err := s.fetchPage(ctx, absURL(s.base, item.url))
				if err != nil {
					// The card already carries title, date, duration, cast and
					// thumbnail, so a detail page that will not load costs the
					// description and tags, not the scene.
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.url, err)):
					case <-ctx.Done():
						return
					}
				} else {
					item.description, item.tags = parseDetail(body)
				}
				select {
				case out <- scraper.Scene(item.toScene(s.base, studioURL, now)):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, item := range items {
		select {
		case work <- item:
		case <-ctx.Done():
			return
		}
	}
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

type listItem struct {
	id          string
	title       string
	url         string
	thumbnail   string
	preview     string
	performers  []string
	date        string
	duration    int
	description string
	tags        []string
}

var (
	cardStartRe = regexp.MustCompile(`<div class="update_details" data-setid="(\d+)">`)
	cardURLRe   = regexp.MustCompile(`<a\s+href="([^"]*_vids\.html)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<!-- Title -->\s*<a\s+href="[^"]*"\s*>([^<]*)</a>`)
	// The card offers the same still at four widths and, on newer sets, an mp4
	// loop. 4x first, then down the ladder, so a set that only has the small
	// rendition still gets a thumbnail.
	thumbRes    = []*regexp.Regexp{}
	previewRe   = regexp.MustCompile(`<source\s+src="([^"]+\.mp4)"`)
	modelsRe    = regexp.MustCompile(`(?s)<span class="update_models">(.*?)</span>`)
	durationRe  = regexp.MustCompile(`(\d+)(?:&nbsp;|\s)+min(?:&nbsp;|\s)+of(?:&nbsp;|\s)+video`)
	dateRe      = regexp.MustCompile(`<!-- Date -->\s*(\d{2}/\d{2}/\d{4})`)
	pagerRe     = regexp.MustCompile(`(?s)<div class="global_pagination">(.*?)</div>`)
	pagerNumRe  = regexp.MustCompile(`(?:_(\d+)_[a-z]\.html|[?&]page=(\d+))`)
	modelIDRe   = regexp.MustCompile(`sets\.php\?id=(\d+)`)
	descRe      = regexp.MustCompile(`(?s)<span class="update_description">(.*?)</span>`)
	tagBlockRe  = regexp.MustCompile(`(?s)<span class="update_tags">(.*?)</span>`)
	tagAnchorRe = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	tagStripRe  = regexp.MustCompile(`<[^>]*>`)
)

func init() {
	for _, attr := range []string{"src0_4x", "poster_4x", "src0_3x", "poster_3x", "src0_2x", "poster_2x", "src0_1x", "poster_1x"} {
		thumbRes = append(thumbRes, regexp.MustCompile(attr+`="([^"]+)"`))
	}
}

func parseListing(body []byte) []listItem {
	locs := cardStartRe.FindAllSubmatchIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		item := listItem{id: string(body[loc[2]:loc[3]])}
		if m := cardURLRe.FindSubmatch(block); m != nil {
			item.url = string(m[1])
		}
		if m := cardTitleRe.FindSubmatch(block); m != nil {
			item.title = cleanText(string(m[1]))
		}
		for _, re := range thumbRes {
			if m := re.FindSubmatch(block); m != nil {
				item.thumbnail = string(m[1])
				break
			}
		}
		if m := previewRe.FindSubmatch(block); m != nil {
			item.preview = string(m[1])
		}
		if m := modelsRe.FindSubmatch(block); m != nil {
			item.performers = parseModels(string(m[1]))
		}
		if m := durationRe.FindSubmatch(block); m != nil {
			mins, _ := strconv.Atoi(string(m[1]))
			item.duration = mins * 60
		}
		if m := dateRe.FindSubmatch(block); m != nil {
			item.date = string(m[1])
		}
		if item.url == "" || item.title == "" {
			continue
		}
		items = append(items, item)
	}
	return items
}

// parseModels splits the cast line. It is plain text on the listing and
// "Featuring: A , B" on a detail page, with the names sometimes linked.
func parseModels(raw string) []string {
	raw = tagStripRe.ReplaceAllString(raw, " ")
	raw = strings.TrimPrefix(cleanText(raw), "Featuring:")
	var names []string
	for _, part := range strings.Split(raw, ",") {
		if n := cleanText(part); n != "" {
			names = append(names, n)
		}
	}
	return names
}

func parseDetail(body []byte) (description string, tags []string) {
	if m := descRe.FindSubmatch(body); m != nil {
		description = cleanText(tagStripRe.ReplaceAllString(string(m[1]), " "))
	}
	if m := tagBlockRe.FindSubmatch(body); m != nil {
		seen := make(map[string]bool)
		for _, a := range tagAnchorRe.FindAllSubmatch(m[1], -1) {
			t := cleanText(string(a[1]))
			if t == "" || seen[strings.ToLower(t)] {
				continue
			}
			seen[strings.ToLower(t)] = true
			tags = append(tags, t)
		}
	}
	return description, tags
}

// estimateTotal multiplies the highest page number the pager names by the
// number of cards on this page. The CMS prints no scene count anywhere.
func estimateTotal(body []byte, perPage int) int {
	last := maxPage(body)
	if last <= 0 {
		return perPage
	}
	return last * perPage
}

func hasNextPage(body []byte, page int) bool {
	return maxPage(body) > page
}

func maxPage(body []byte) int {
	pm := pagerRe.FindSubmatch(body)
	if pm == nil {
		return 0
	}
	last := 0
	for _, m := range pagerNumRe.FindAllSubmatch(pm[1], -1) {
		digits := m[1]
		if len(digits) == 0 {
			digits = m[2]
		}
		if n, err := strconv.Atoi(string(digits)); err == nil && n > last {
			last = n
		}
	}
	return last
}

func extractModelID(body []byte) string {
	if m := modelIDRe.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (item listItem) toScene(base, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          item.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         absURL(base, item.url),
		Description: item.description,
		Thumbnail:   absURL(base, item.thumbnail),
		Preview:     absURL(base, item.preview),
		Performers:  item.performers,
		Studio:      studioName,
		Tags:        item.tags,
		Duration:    item.duration,
		ScrapedAt:   now,
	}
	if d, err := parseutil.TryParseDate(item.date, "01/02/2006"); err == nil {
		sc.Date = d
	}
	return sc
}

func absURL(base, u string) string {
	if u == "" || strings.HasPrefix(u, "http") {
		return u
	}
	return base + "/" + strings.TrimPrefix(u, "/")
}
