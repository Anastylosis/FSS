package porncz

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
	defaultBase = "https://www.porncz.com"
	siteID      = "porncz"
	studioName  = "PornCZ"
	perPage     = 30
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?porncz\.com(?:/|$)`)

type Scraper struct {
	client *http.Client
	base   string
}

// RecommendedDelay is the pace this scraper was written against. It is **not**
// silently enforced — the operator's `opts.Delay` is always honoured, including
// `--delay 0` — but `WarnDelayBelow` surfaces a one-shot stderr warning below it.
const RecommendedDelay = 500 * time.Millisecond

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"porncz.com",
		"porncz.com/en/pornstars/{slug}",
		"porncz.com/en/free-trailers/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listItem struct {
	slug  string
	title string
	thumb string
	site  string
}

var (
	pornstarPathRe = regexp.MustCompile(`/en/pornstars/([a-z0-9-]+)`)
	trailersPathRe = regexp.MustCompile(`/en/free-trailers/([a-z0-9-]+)`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	delay := opts.Delay
	scraper.WarnDelayBelow("porncz", opts.Delay, RecommendedDelay)
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listItem, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if !scraper.Pace(ctx, delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, item, studioURL)
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

	if pornstarPathRe.MatchString(studioURL) || trailersPathRe.MatchString(studioURL) {
		s.scrapeSinglePage(ctx, studioURL, opts, out, work)
	} else {
		s.scrapePaginated(ctx, opts, out, work, delay)
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) scrapeSinglePage(ctx context.Context, pageURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listItem) {
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	items := parseListingCards(body)
	if len(items) == 0 {
		return
	}

	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	for _, item := range items {
		if opts.KnownIDs[item.slug] {
			scraper.Debugf(1, "porncz: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case work <- item:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) scrapePaginated(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listItem, delay time.Duration) {
	totalSent := false

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}

		if page > 1 && !scraper.Pace(ctx, delay) {
			return
		}
		scraper.Debugf(1, "porncz: fetching page %d", page)

		pageURL := fmt.Sprintf("%s/en/videos?sort=new&page=%d", s.base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		items := parseListingCards(body)
		if len(items) == 0 {
			return
		}

		if !totalSent {
			total := parseTotalPages(body) * perPage
			if total > 0 {
				scraper.Debugf(1, "porncz: %d total scenes", total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
			totalSent = true
		}

		for _, item := range items {
			if opts.KnownIDs[item.slug] {
				scraper.Debugf(1, "porncz: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- item:
			case <-ctx.Done():
				return
			}
		}

		if len(items) < perPage {
			return
		}
	}
}

var (
	cardStartRe = regexp.MustCompile(`(?s)class="card card--item video-thumbnails"`)
	slugRe      = regexp.MustCompile(`href="/en/([a-z0-9][a-z0-9-]*)" class="card__link`)
	thumbRe     = regexp.MustCompile(`data-src="(https://img2\.porncz\.com/[^"]+)"`)
	altRe       = regexp.MustCompile(`alt="([^"]+)"`)
	siteRe      = regexp.MustCompile(`icon-website[^<]*</i>\s*([a-z0-9.-]+\.\w+)`)
)

func parseListingCards(body []byte) []listItem {
	starts := cardStartRe.FindAllIndex(body, -1)
	seen := map[string]bool{}
	var items []listItem

	for i, loc := range starts {
		end := len(body)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		card := body[loc[0]:end]

		m := slugRe.FindSubmatch(card)
		if m == nil {
			continue
		}
		slug := string(m[1])
		if seen[slug] {
			continue
		}
		seen[slug] = true

		item := listItem{slug: slug}

		if tm := thumbRe.FindSubmatch(card); tm != nil {
			item.thumb = string(tm[1])
		}
		if am := altRe.FindSubmatch(card); am != nil {
			item.title = html.UnescapeString(string(am[1]))
		}
		if sm := siteRe.FindSubmatch(card); sm != nil {
			item.site = string(sm[1])
		}

		items = append(items, item)
	}
	return items
}

var maxPageRe = regexp.MustCompile(`page=(\d+)`)

func parseTotalPages(body []byte) int {
	maxPage := 0
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

func (s *Scraper) fetchDetail(ctx context.Context, item listItem, studioURL string) (models.Scene, error) {
	sceneURL := s.base + "/en/" + item.slug
	body, err := s.fetchPage(ctx, sceneURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", item.slug, err)
	}

	vo := parseutil.ExtractVideoObject(body)
	if vo == nil {
		return models.Scene{}, fmt.Errorf("detail %s: no VideoObject JSON-LD", item.slug)
	}

	title := strings.TrimSpace(vo.Name)
	if title == "" {
		title = item.title
	}

	var date time.Time
	if vo.UploadDate != "" {
		if t, err := time.Parse(time.RFC3339, vo.UploadDate); err == nil {
			date = t.UTC()
		} else if t, err := time.Parse("2006-01-02", vo.UploadDate[:min(len(vo.UploadDate), 10)]); err == nil {
			date = t.UTC()
		}
	}

	var performers []string
	for _, name := range vo.Actors {
		name = strings.TrimSpace(name)
		if name != "" {
			performers = append(performers, name)
		}
	}

	thumb := vo.ThumbnailURL
	if thumb == "" {
		thumb = item.thumb
	}

	now := time.Now().UTC()
	return models.Scene{
		ID:          item.slug,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       title,
		URL:         sceneURL,
		Date:        date,
		Description: strings.TrimSpace(vo.Description),
		Thumbnail:   thumb,
		Performers:  performers,
		Studio:      studioName,
		Series:      strings.TrimSpace(vo.PartOfSeries),
		Duration:    parseutil.ParseDurationISO(vo.Duration),
		ScrapedAt:   now,
	}, nil
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
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
