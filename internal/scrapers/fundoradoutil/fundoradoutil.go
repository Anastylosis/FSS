// Package fundoradoutil scrapes Fundorado white-label VOD sites, a React shell
// over api.fundorado.com. A site row names the host and the label ids that make
// up its catalogue; the API serves them through /api/videos/browse/{kind}/{id}.
package fundoradoutil

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	DefaultAPIBase = "https://api.fundorado.com"
	pageSize       = 50
	defaultWorkers = 4
)

type SiteConfig struct {
	SiteID     string
	Host       string // bare domain, no www
	StudioName string
	Labels     []int // label ids walked for the site root
}

type Scraper struct {
	cfg     SiteConfig
	Client  *http.Client
	APIBase string
	matchRe *regexp.Regexp
}

func New(cfg SiteConfig) *Scraper {
	host := strings.ReplaceAll(cfg.Host, ".", `\.`)
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		APIBase: DefaultAPIBase,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + host + `(?:/(?:videos/browse/(?:labels|studio|genres?)/\d+)?/?)?(?:[?#].*)?$`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	h := s.cfg.Host
	return []string{
		h,
		h + "/videos/browse/labels/{id}",
		h + "/videos/browse/studio/{id}",
		h + "/videos/browse/genres/{id}",
		h + "/videos/browse/genre/{id}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type browseTarget struct {
	kind string
	id   int
}

var browseRe = regexp.MustCompile(`/videos/browse/(labels|studio|genres?)/(\d+)`)

// targets maps the URL to the browse lists it covers: the site's label set for
// the root, a single list for a /videos/browse/ URL.
func (s *Scraper) targets(studioURL string) []browseTarget {
	if m := browseRe.FindStringSubmatch(studioURL); m != nil {
		id, _ := strconv.Atoi(m[2])
		return []browseTarget{{kind: m[1], id: id}}
	}
	t := make([]browseTarget, len(s.cfg.Labels))
	for i, id := range s.cfg.Labels {
		t[i] = browseTarget{kind: "labels", id: id}
	}
	return t
}

type siteConfig struct {
	ID int `json:"id"`
}

type browseResponse struct {
	Videos struct {
		Data     []videoItem `json:"data"`
		Total    int         `json:"total"`
		LastPage int         `json:"last_page"`
	} `json:"videos"`
}

type videoItem struct {
	ID              int         `json:"id"`
	Title           langString  `json:"title"`
	Slug            string      `json:"slug"`
	Artwork         artworkObj  `json:"artwork"`
	Cover           artworkObj  `json:"cover"`
	PublicationDate string      `json:"publication_date"`
	Actors          []actorItem `json:"actors"`
	Meta            struct {
		DurationSeconds int `json:"duration_seconds"`
	} `json:"meta"`
	Views int `json:"views_total"`
}

type artworkObj struct {
	Original string `json:"original"`
}

type actorItem struct {
	Name string `json:"name"`
}

type detailResponse struct {
	Video struct {
		Description      langString `json:"description"`
		TransDescription langString `json:"trans_description"`
		Genres           []struct {
			Title langString `json:"title"`
		} `json:"genres"`
	} `json:"video"`
}

// langString is a per-language text object. Catalogues usually carry a single
// language, so the key set is not predictable.
type langString map[string]string

func (ls langString) key() string {
	if ls["en"] != "" {
		return "en"
	}
	keys := make([]string, 0, len(ls))
	for k, v := range ls {
		if v != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	return keys[0]
}

func (ls langString) String() string { return strings.TrimSpace(ls[ls.key()]) }

func (s *Scraper) headers() map[string]string {
	return map[string]string{
		"User-Agent": httpx.UserAgentFirefox,
		"Origin":     "https://" + s.cfg.Host,
		"Referer":    "https://" + s.cfg.Host + "/",
	}
}

func (s *Scraper) getJSON(ctx context.Context, path string, v any) error {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: s.APIBase + path, Headers: s.headers()})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.DecodeJSON(resp.Body, v)
}

func (s *Scraper) fetchSiteID(ctx context.Context) (int, error) {
	var cfg siteConfig
	if err := s.getJSON(ctx, "/api/sitecfg?h="+s.cfg.Host, &cfg); err != nil {
		return 0, err
	}
	if cfg.ID == 0 {
		return 0, scraper.ParseError(s.APIBase+"/api/sitecfg", fmt.Errorf("no site id for %s", s.cfg.Host))
	}
	return cfg.ID, nil
}

func (s *Scraper) fetchBrowse(ctx context.Context, siteID int, t browseTarget, page int) (*browseResponse, error) {
	var resp browseResponse
	path := fmt.Sprintf("/api/videos/browse/%s/%d?site_id=%d&page=%d&lang=en&sg=0&video_type=scene", t.kind, t.id, siteID, page)
	if err := s.getJSON(ctx, path, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *Scraper) fetchDetail(ctx context.Context, id int) (*detailResponse, error) {
	var resp detailResponse
	if err := s.getJSON(ctx, "/api/videodetail/"+strconv.Itoa(id), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	targets := s.targets(studioURL)
	scraper.Debugf(1, "%s: %d browse list(s) for %s", s.cfg.SiteID, len(targets), studioURL)

	siteID, err := s.fetchSiteID(ctx)
	if err != nil {
		send(ctx, out, scraper.Error(fmt.Errorf("fetching site config: %w", err)))
		return
	}

	// Page 1 of every list is fetched up front so one progress total covers
	// the whole walk; Paginate reuses it instead of fetching again.
	firsts := make([]*browseResponse, len(targets))
	total := 0
	for i, t := range targets {
		if i > 0 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		resp, err := s.fetchBrowse(ctx, siteID, t, 1)
		if err != nil {
			if !send(ctx, out, scraper.Error(fmt.Errorf("%s %d: %w", t.kind, t.id, err))) {
				return
			}
			continue
		}
		firsts[i] = resp
		total += resp.Videos.Total
	}
	if total > 0 && !send(ctx, out, scraper.Progress(total)) {
		return
	}

	seen := map[int]bool{}
	for i, t := range targets {
		if firsts[i] == nil {
			continue
		}
		first := firsts[i]
		scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
			resp := first
			if page > 1 {
				var err error
				if resp, err = s.fetchBrowse(ctx, siteID, t, page); err != nil {
					return scraper.PageResult{}, err
				}
			}
			var items []videoItem
			for _, it := range resp.Videos.Data {
				if !seen[it.ID] {
					seen[it.ID] = true
					items = append(items, it)
				}
			}
			scenes := s.buildScenes(ctx, items, studioURL, opts, out)
			return scraper.PageResult{
				Scenes:   scenes,
				Done:     page >= resp.Videos.LastPage,
				Continue: len(resp.Videos.Data) > 0,
			}, nil
		})
		if ctx.Err() != nil {
			return
		}
	}
}

// buildScenes fetches each item's detail for the description and genres. A
// detail that will not load costs only those two fields, so the error is
// reported and the listing data still yields the scene.
func (s *Scraper) buildScenes(ctx context.Context, items []videoItem, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) []models.Scene {
	if len(items) == 0 {
		return nil
	}
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)

	details := make([]*detailResponse, len(items))
	errs := make([]error, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, it := range items {
		wg.Add(1)
		go func(i int, id int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			details[i], errs[i] = s.fetchDetail(ctx, id)
		}(i, it.ID)
	}
	wg.Wait()

	now := time.Now().UTC()
	scenes := make([]models.Scene, 0, len(items))
	for i, it := range items {
		if errs[i] != nil {
			if !send(ctx, out, scraper.Error(fmt.Errorf("detail %d: %w", it.ID, errs[i]))) {
				return nil
			}
		}
		scenes = append(scenes, s.toScene(it, details[i], studioURL, now))
	}
	return scenes
}

func (s *Scraper) toScene(it videoItem, detail *detailResponse, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:        strconv.Itoa(it.ID),
		SiteID:    s.cfg.SiteID,
		StudioURL: studioURL,
		Title:     it.Title.String(),
		URL:       fmt.Sprintf("https://%s/video/%d/%s", s.cfg.Host, it.ID, it.Slug),
		Thumbnail: it.Artwork.Original,
		Date:      parseDate(it.PublicationDate),
		Duration:  it.Meta.DurationSeconds,
		Studio:    s.cfg.StudioName,
		Views:     it.Views,
		ScrapedAt: now,
	}
	if sc.Thumbnail == "" {
		sc.Thumbnail = it.Cover.Original
	}
	for _, a := range it.Actors {
		if a.Name != "" {
			sc.Performers = append(sc.Performers, a.Name)
		}
	}
	if detail != nil {
		sc.Description = description(detail, it.Title.key())
		for _, g := range detail.Video.Genres {
			if t := g.Title.String(); t != "" {
				sc.Tags = append(sc.Tags, t)
			}
		}
	}
	return sc
}

// description keeps to the title's language so the two read together; the
// machine translation in trans_description only fills a gap.
func description(d *detailResponse, lang string) string {
	if t := strings.TrimSpace(d.Video.Description[lang]); t != "" {
		return t
	}
	if t := strings.TrimSpace(d.Video.TransDescription[lang]); t != "" {
		return t
	}
	if t := d.Video.Description.String(); t != "" {
		return t
	}
	return d.Video.TransDescription.String()
}

// parseDate reads the API's UTC timestamp. Releases are stamped at Berlin
// midnight (22:00Z or 23:00Z the day before), so half a day on and truncating
// recovers the release day in either season.
func parseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.Add(12 * time.Hour).Truncate(24 * time.Hour)
}
