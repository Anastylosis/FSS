package manyvids

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	defaultAPIBase  = "https://api.manyvids.com"
	defaultSiteBase = "https://www.manyvids.com"
)

// Scraper implements scraper.StudioScraper for ManyVids.
type Scraper struct {
	client   *http.Client
	apiBase  string
	siteBase string
}

func New() *Scraper {
	return &Scraper{
		client:   httpx.NewClient(30 * time.Second),
		apiBase:  defaultAPIBase,
		siteBase: defaultSiteBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

// ---- StudioScraper interface ----

func (s *Scraper) ID() string { return "manyvids" }

func (s *Scraper) Patterns() []string {
	return []string{
		"manyvids.com/Profile/{creatorId}/{slug}/Store/Videos",
	}
}

var profileRe = regexp.MustCompile(`^https?://(?:www\.)?manyvids\.com/Profile/(\d+)/[^/]+/Store/Videos`)

func (s *Scraper) MatchesURL(u string) bool {
	return profileRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	cid, err := creatorID(studioURL)
	if err != nil {
		return nil, err
	}
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, cid, opts, out)
	return out, nil
}

// ---- internal types ----

type listEntry struct {
	id         string
	previewURL string
}

// ---- worker orchestration ----

func (s *Scraper) run(ctx context.Context, studioURL, cid string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	work := make(chan listEntry, opts.Workers)
	var wg sync.WaitGroup

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, studioURL, entry.id, entry.previewURL)
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

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			break
		}
		scraper.Debugf(1, "manyvids: fetching page %d", page)
		entries, totalPages, err := s.fetchPage(ctx, cid, page)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}
		if page == 1 && totalPages > 0 && len(entries) > 0 {
			cancelled := false
			select {
			case out <- scraper.Progress(totalPages * len(entries)):
			case <-ctx.Done():
				cancelled = true
			}
			if cancelled {
				break
			}
		}
		cancelled := false
		hitKnown := false
		for _, e := range entries {
			if opts.KnownIDs[e.id] {
				// Content is sorted newest-first; a known ID means everything
				// from this point is already stored.
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
		if cancelled || hitKnown || page >= totalPages {
			if hitKnown {
				scraper.Debugf(1, "manyvids: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
			}
			break
		}
	}

	close(work)
	wg.Wait()
}

// ---- API calls ----

func (s *Scraper) fetchPage(ctx context.Context, cid string, page int) ([]listEntry, int, error) {
	u := fmt.Sprintf("%s/store/videos/%s?sort=date&page=%d", s.apiBase, cid, page)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	var lr listResponse
	if err := httpx.DecodeJSON(resp.Body, &lr); err != nil {
		return nil, 0, fmt.Errorf("decoding list response: %w", err)
	}

	entries := make([]listEntry, len(lr.Data))
	for i, item := range lr.Data {
		entries[i] = listEntry{id: item.ID, previewURL: item.Preview.URL}
	}
	return entries, lr.Pagination.TotalPages, nil
}

func (s *Scraper) fetchDetail(ctx context.Context, studioURL, id, previewURL string) (models.Scene, error) {
	u := fmt.Sprintf("%s/store/video/%s", s.apiBase, id)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return models.Scene{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	var dr detailResponse
	if err := httpx.DecodeJSON(resp.Body, &dr); err != nil {
		return models.Scene{}, fmt.Errorf("decoding detail for %s: %w", id, err)
	}

	return toScene(studioURL, s.siteBase, dr.Data, previewURL, time.Now().UTC())
}

// ---- mapping ----

func toScene(studioURL, siteBase string, item detailItem, previewURL string, now time.Time) (models.Scene, error) {
	tags := make([]string, len(item.TagList))
	for i, t := range item.TagList {
		tags[i] = t.Label
	}

	regular, regularErr := strconv.ParseFloat(item.Price.Regular, 64)
	discounted, _ := strconv.ParseFloat(item.Price.DiscountedPrice, 64)

	scene := models.Scene{
		ID:          item.ID,
		SiteID:      "manyvids",
		StudioURL:   studioURL,
		Title:       item.Title,
		URL:         siteBase + item.URL,
		Date:        parseDate(item.LaunchDate),
		Description: html.UnescapeString(item.Description),
		Thumbnail:   item.Screenshot,
		Preview:     previewURL,
		Performers:  []string{item.Model.DisplayName},
		Studio:      item.Model.DisplayName,
		Tags:        tags,
		Duration:    parseutil.ParseDurationColon(item.VideoDuration),
		Resolution:  item.Resolution,
		Width:       item.Width,
		Height:      item.Height,
		Format:      item.Extension,
		Views:       item.ViewsRaw,
		Likes:       item.LikesRaw,
		Comments:    item.Comments,
		ScrapedAt:   now,
	}

	if item.Price.Free || regularErr == nil {
		scene.AddPrice(models.PriceSnapshot{
			Date:            now,
			Regular:         regular,
			Discounted:      discounted,
			IsFree:          item.Price.Free,
			IsOnSale:        item.Price.OnSale,
			DiscountPercent: item.Price.PromoRate,
		})
	}

	return scene, nil
}

// ---- helpers ----

func creatorID(studioURL string) (string, error) {
	m := profileRe.FindStringSubmatch(studioURL)
	if m == nil {
		return "", fmt.Errorf("cannot extract creator ID from %q", studioURL)
	}
	return m[1], nil
}

// parseDate parses ManyVids API timestamps (RFC3339 with optional milliseconds).
func parseDate(s string) time.Time {
	t, _ := parseutil.TryParseDate(s, time.RFC3339Nano, time.RFC3339)
	return t.UTC()
}

// ---- API response types ----

type listResponse struct {
	StatusCode int        `json:"statusCode"`
	Data       []listItem `json:"data"`
	Pagination pagination `json:"pagination"`
}

type listItem struct {
	ID      string `json:"id"`
	Preview struct {
		URL string `json:"url"`
	} `json:"preview"`
}

type pagination struct {
	TotalPages  int `json:"totalPages"`
	CurrentPage int `json:"currentPage"`
	NextPage    int `json:"nextPage"`
}

type detailResponse struct {
	StatusCode int        `json:"statusCode"`
	Data       detailItem `json:"data"`
}

type detailItem struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	LaunchDate    string  `json:"launchDate"`
	VideoDuration string  `json:"videoDuration"`
	Description   string  `json:"description"`
	TagList       []mvTag `json:"tagList"`
	Thumbnail     string  `json:"thumbnail"`
	Screenshot    string  `json:"screenshot"`
	Model         mvModel `json:"model"`
	Resolution    string  `json:"resolution"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`
	Extension     string  `json:"extension"`
	URL           string  `json:"url"`
	ViewsRaw      int     `json:"viewsRaw"`
	LikesRaw      int     `json:"likesRaw"`
	Comments      int     `json:"comments"`
	Price         mvPrice `json:"price"`
}

type mvTag struct {
	Label string `json:"label"`
}

type mvModel struct {
	DisplayName string `json:"displayName"`
}

type mvPrice struct {
	Free            bool   `json:"free"`
	OnSale          bool   `json:"onSale"`
	Regular         string `json:"regular"`
	DiscountedPrice string `json:"discountedPrice"`
	PromoRate       int    `json:"promoRate"`
}
