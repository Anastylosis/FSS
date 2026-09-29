package sexlikereal

import (
	"context"
	"fmt"
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

const (
	apiBase  = "https://api.sexlikereal.com"
	siteBase = "https://www.sexlikereal.com"
	// perPage is the API's hard maximum: `perPage=37` returns HTTP 422
	// ("should be less than or equal to 36"). The full catalogue is therefore
	// ~1,666 list pages, and there is no cursor or alternate sort to shorten
	// the walk (`sort` accepts only "recent"; oldest/id/popular all 422).
	perPage = 36

	// maxPageWorkers bounds the parallel list-page walk. The listing endpoint is
	// far more expensive upstream than the per-scene detail endpoint — an
	// uncached page can take 30s while a detail fetch takes ~100ms — so the page
	// pool is capped independently of --workers rather than scaling with it.
	maxPageWorkers = 8
)

// RecommendedDelay is a conservative minimum delay between requests, inferred
// from operator reports rather than a published rate limit. It is **not**
// silently enforced — the operator's `opts.Delay` is always honoured — but
// `WarnDelayBelow` surfaces a one-shot stderr warning when the configured delay
// is lower.
//
// Delay is not the main lever here, though. What bounds a full scrape is
// upstream listing latency, which is severely fat-tailed: over 60 uncached pages
// the median was 0.96s but the mean was 28s. A stalled page costs ~96s, since
// the gateway gives up at ~30s and httpx retries twice on top. The stall rate
// also rises with concurrency, so pushing harder makes it worse — see
// maxPageWorkers.
const RecommendedDelay = 300 * time.Millisecond

type Scraper struct {
	client     *http.Client
	apiBaseURL string
}

// requestTimeout must sit above the API's own ~30s gateway ceiling. A list page
// that misses the upstream cache is computed at the origin and can return at
// 30.4s, or be given up on by the gateway as an HTTP 500 at exactly 30.3s. A 30s
// client timeout races that ceiling and cancels responses that were about to
// arrive, turning a slow page into a failed one. 60s lets the late-but-valid
// responses land and leaves the gateway, not us, to decide when to give up.
const requestTimeout = 60 * time.Second

func New() *Scraper {
	return &Scraper{
		client:     httpx.NewClient(requestTimeout),
		apiBaseURL: apiBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "sexlikereal" }

func (s *Scraper) Patterns() []string {
	return []string{
		"sexlikereal.com",
		"sexlikereal.com/scenes",
		"sexlikereal.com/studios/{slug}-{id}",
		"sexlikereal.com/pornstars/{slug}-{id}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?sexlikereal\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type filterMode int

const (
	filterAll filterMode = iota
	filterStudio
	filterModel
)

var (
	studioRe = regexp.MustCompile(`/studios/[^/]+-(\d+)`)
	modelRe  = regexp.MustCompile(`/pornstars/[^/]+-(\d+)`)
)

func resolveFilter(studioURL string) (filterMode, string) {
	if m := studioRe.FindStringSubmatch(studioURL); m != nil {
		return filterStudio, m[1]
	}
	if m := modelRe.FindStringSubmatch(studioURL); m != nil {
		return filterModel, m[1]
	}
	return filterAll, ""
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	mode, filterID := resolveFilter(studioURL)
	scraper.WarnDelayBelow(s.ID(), opts.Delay, RecommendedDelay)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan apiScene)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				scene, err := s.fetchAndBuild(ctx, item, studioURL, opts.Delay)
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
		s.walkPages(ctx, mode, filterID, opts, out, work)
	}()

	wg.Wait()
}

// listURL builds the listing request for one page under the active filter.
func (s *Scraper) listURL(mode filterMode, filterID string, page int) string {
	params := url.Values{
		"page":    {strconv.Itoa(page)},
		"perPage": {strconv.Itoa(perPage)},
		"sort":    {"recent"},
	}
	switch mode {
	case filterStudio:
		params.Set("studios", filterID)
	case filterModel:
		params.Set("models", filterID)
	}
	return s.apiBaseURL + "/v3/scenes?" + params.Encode()
}

// walkPages traverses the listing and feeds every scene stub into work.
//
// It picks one of two strategies. An incremental scrape (KnownIDs populated)
// walks pages in order so the date-sorted early-stop is meaningful — stopping
// at the first known ID is the whole point, and it usually means only a handful
// of pages get fetched. A full traversal (--full / --refresh, no KnownIDs) has
// no early-stop to preserve and must fetch every page regardless of order, so
// it fans the walk out over a worker pool.
//
// The distinction matters because the listing walk, not the detail fetch, is
// what bounds a full scrape: perPage is capped at 36, so the catalogue is ~1,666
// pages, and a serial walk pays every cold-page stall end to end. --workers only
// ever parallelised the detail fetches, which are not the bottleneck.
func (s *Scraper) walkPages(ctx context.Context, mode filterMode, filterID string, opts scraper.ListOpts,
	out chan<- scraper.SceneResult, work chan<- apiScene,
) {
	if len(opts.KnownIDs) > 0 {
		s.walkSerial(ctx, mode, filterID, opts, out, work)
		return
	}
	s.walkParallel(ctx, mode, filterID, opts, out, work)
}

// walkSerial is the ordered walk used for incremental scrapes, where hitting a
// known ID must stop the traversal.
func (s *Scraper) walkSerial(ctx context.Context, mode filterMode, filterID string, opts scraper.ListOpts,
	out chan<- scraper.SceneResult, work chan<- apiScene,
) {
	totalPages := 0
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "sexlikereal: fetching page %d", page)

		var resp listResponse
		if err := s.fetchJSON(ctx, s.listURL(mode, filterID, page), &resp); err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
				return
			}
			// Once totalPages is known, a transient failure on a single
			// page must not abandon the rest of the traversal — skip it
			// and keep going so one bad page doesn't lose thousands of
			// unreached scenes. Page 1 failing is still fatal: with no
			// totalPages there is no way to know how many pages remain.
			if totalPages > 0 && page < totalPages {
				continue
			}
			return
		}

		if resp.Meta.Pagination.TotalPages > 0 {
			totalPages = resp.Meta.Pagination.TotalPages
		}

		if page == 1 && resp.Meta.Pagination.TotalCount > 0 {
			select {
			case out <- scraper.Progress(resp.Meta.Pagination.TotalCount):
			case <-ctx.Done():
				return
			}
		}

		if len(resp.Data) == 0 {
			return
		}

		for _, item := range resp.Data {
			id := strconv.Itoa(item.ID)
			if opts.KnownIDs[id] {
				scraper.Debugf(1, "sexlikereal: hit known ID, stopping early")
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

		if page >= resp.Meta.Pagination.TotalPages {
			return
		}
	}
}

// walkParallel fetches page 1 to learn the page count, then fans the remaining
// pages out over a bounded pool. Used only when there is no KnownIDs early-stop
// to preserve, so page order carries no meaning.
func (s *Scraper) walkParallel(ctx context.Context, mode filterMode, filterID string, opts scraper.ListOpts,
	out chan<- scraper.SceneResult, work chan<- apiScene,
) {
	sendScenes := func(items []apiScene) bool {
		for _, item := range items {
			select {
			case work <- item:
			case <-ctx.Done():
				return false
			}
		}
		return true
	}

	scraper.Debugf(1, "sexlikereal: fetching page 1")
	var first listResponse
	if err := s.fetchJSON(ctx, s.listURL(mode, filterID, 1), &first); err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("page 1: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	if first.Meta.Pagination.TotalCount > 0 {
		select {
		case out <- scraper.Progress(first.Meta.Pagination.TotalCount):
		case <-ctx.Done():
			return
		}
	}

	if len(first.Data) == 0 {
		return
	}
	if !sendScenes(first.Data) {
		return
	}

	totalPages := first.Meta.Pagination.TotalPages
	if totalPages <= 1 {
		return
	}

	pageWorkers := opts.Workers
	if pageWorkers <= 0 || pageWorkers > maxPageWorkers {
		pageWorkers = maxPageWorkers
	}
	scraper.Debugf(1, "sexlikereal: walking pages 2-%d with %d page workers", totalPages, pageWorkers)

	pages := make(chan int)
	var pwg sync.WaitGroup
	for i := 0; i < pageWorkers; i++ {
		pwg.Add(1)
		go func() {
			defer pwg.Done()
			for page := range pages {
				if ctx.Err() != nil {
					return
				}
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scraper.Debugf(1, "sexlikereal: fetching page %d", page)

				var resp listResponse
				if err := s.fetchJSON(ctx, s.listURL(mode, filterID, page), &resp); err != nil {
					select {
					case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
					case <-ctx.Done():
						return
					}
					// One bad page must not abandon the rest of the walk; the
					// error already marks the traversal incomplete so the cmd
					// layer falls back to non-destructive merge semantics.
					continue
				}
				if !sendScenes(resp.Data) {
					return
				}
			}
		}()
	}

	for page := 2; page <= totalPages; page++ {
		select {
		case pages <- page:
		case <-ctx.Done():
			close(pages)
			pwg.Wait()
			return
		}
	}
	close(pages)
	pwg.Wait()
}

func (s *Scraper) fetchAndBuild(ctx context.Context, item apiScene, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	apiURL := s.apiBaseURL + "/v3/scenes/" + strconv.Itoa(item.ID)
	var resp detailResponse
	if err := s.fetchJSON(ctx, apiURL, &resp); err != nil {
		return models.Scene{}, fmt.Errorf("detail %d: %w", item.ID, err)
	}

	return toScene(item, resp.Data, studioURL), nil
}

func toScene(item apiScene, detail detailData, studioURL string) models.Scene {
	sc := models.Scene{
		ID:          strconv.Itoa(item.ID),
		SiteID:      "sexlikereal",
		StudioURL:   studioURL,
		Title:       item.Title,
		URL:         siteBase + "/scenes/" + item.Label,
		Thumbnail:   item.ThumbnailURL,
		Description: strings.TrimSpace(item.Description),
		Duration:    item.FullVideoLength,
		ScrapedAt:   time.Now().UTC(),
	}

	if item.Date > 0 {
		sc.Date = time.Unix(int64(item.Date), 0).UTC()
	}

	if item.Studio.Name != "" {
		sc.Studio = item.Studio.Name
	}

	for _, a := range item.Actors {
		sc.Performers = append(sc.Performers, a.Name)
	}

	for _, c := range detail.Categories {
		sc.Tags = append(sc.Tags, c.Name)
	}

	if detail.Price.Amount > 0 {
		sc.AddPrice(models.PriceSnapshot{
			Date:    time.Now().UTC(),
			Regular: detail.Price.Amount,
		})
	}

	return sc
}

func (s *Scraper) fetchJSON(ctx context.Context, u string, v any) error {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: u,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Client-Type"] = "web"
			h["Project"] = "1"
			return h
		}(),
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.DecodeJSON(resp.Body, v)
}

type listResponse struct {
	Data []apiScene `json:"data"`
	Meta struct {
		Pagination struct {
			Page       int `json:"page"`
			PerPage    int `json:"perPage"`
			TotalCount int `json:"totalCount"`
			TotalPages int `json:"totalPages"`
		} `json:"pagination"`
	} `json:"meta"`
}

type detailResponse struct {
	Data detailData `json:"data"`
}

type detailData struct {
	Categories []apiCat `json:"categories"`
	Price      apiPrice `json:"price"`
}

type apiScene struct {
	ID              int        `json:"id"`
	Title           string     `json:"title"`
	Label           string     `json:"label"`
	Description     string     `json:"description"`
	Date            int        `json:"date"`
	FullVideoLength int        `json:"fullVideoLength"`
	ThumbnailURL    string     `json:"thumbnailUrl"`
	Studio          apiStudio  `json:"studio"`
	Actors          []apiActor `json:"actors"`
}

type apiStudio struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label"`
}

type apiActor struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label"`
}

type apiCat struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label"`
}

type apiPrice struct {
	Amount float64 `json:"amount"`
}
