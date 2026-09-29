package ayloutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
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
	DefaultAPIHost = "https://site-api.project1service.com"
	HitsPerPage    = 100
)

type SiteConfig struct {
	SiteID     string
	SiteBase   string
	StudioName string
	APIHost    string
	ScenePath  string // URL path segment for scenes (default: "video")
}

type Scraper struct {
	Client  *http.Client
	cfg     SiteConfig
	APIHost string
}

func New(cfg SiteConfig) *Scraper {
	host := DefaultAPIHost
	if cfg.APIHost != "" {
		host = cfg.APIHost
	}
	return &Scraper{
		Client:  httpx.NewClient(30 * time.Second),
		cfg:     cfg,
		APIHost: host,
	}
}

type FilterType int

const (
	FilterAll FilterType = iota
	FilterActor
	FilterCollection
	FilterTag
	FilterSeries
)

type Filter struct {
	Type FilterType
	ID   int
	Slug string // for slug-based lookups that need API resolution
}

var (
	// Actor (performer) URL paths used across Aylo siblings:
	//   /pornstar/<id>/<slug>      — historical, still used on Brazzers
	//   /model/<id>/<slug>         — current on Babes, Mofos, RealityKings
	//   /modelprofile/<id>/<slug>  — current on DigitalPlayground
	// Order matters: longer alternatives first so the regex engine prefers
	// /modelprofile/ over /model/.
	pornstarRe = regexp.MustCompile(`/(?:modelprofile|pornstar|model)/(\d+)`)
	categoryRe = regexp.MustCompile(`/category/(\d+)`)
	siteRe     = regexp.MustCompile(`/(?:site|collection)/(\d+)`)
	seriesRe   = regexp.MustCompile(`/series/(\d+)`)
	siteSlugRe = regexp.MustCompile(`/sites/([a-z0-9-]+)`)
)

func ParseFilter(rawURL string) Filter {
	if m := pornstarRe.FindStringSubmatch(rawURL); m != nil {
		id, _ := strconv.Atoi(m[1])
		return Filter{Type: FilterActor, ID: id}
	}
	if m := categoryRe.FindStringSubmatch(rawURL); m != nil {
		id, _ := strconv.Atoi(m[1])
		return Filter{Type: FilterTag, ID: id}
	}
	if m := siteRe.FindStringSubmatch(rawURL); m != nil {
		id, _ := strconv.Atoi(m[1])
		return Filter{Type: FilterCollection, ID: id}
	}
	if m := seriesRe.FindStringSubmatch(rawURL); m != nil {
		id, _ := strconv.Atoi(m[1])
		return Filter{Type: FilterSeries, ID: id}
	}
	if u, err := url.Parse(rawURL); err == nil {
		if tagID := u.Query().Get("tags"); tagID != "" {
			if id, err := strconv.Atoi(tagID); err == nil {
				return Filter{Type: FilterTag, ID: id}
			}
		}
	}
	if m := siteSlugRe.FindStringSubmatch(rawURL); m != nil {
		return Filter{Type: FilterCollection, Slug: m[1]}
	}
	return Filter{Type: FilterAll}
}

func (s *Scraper) FetchToken(ctx context.Context) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     s.cfg.SiteBase,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", fmt.Errorf("fetching instance token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	for _, c := range resp.Cookies() {
		if c.Name == "instance_token" {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("instance_token cookie not found")
}

// resolveCollectionSlug maps a /sites/{slug} URL onto a collection id.
//
// The API publishes no slug of its own, only the display name, so the slug has
// to be matched against a derivation of that name — and the site's own
// derivation is not quite `slugify`'s: Reality Kings serves "Mike's Apartment"
// at /sites/mike-s-apartment, where the apostrophe became a separator rather
// than being dropped. An exact `slugify` match is therefore tried first, then a
// loose one that ignores separators entirely on both sides, which agrees with
// every spelling the tours actually link.
func (s *Scraper) resolveCollectionSlug(ctx context.Context, token string, slug string) (int, error) {
	type collection struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	var all []collection

	// The listing is paged: a brand with more than one page of collections used
	// to have everything past the first silently unresolvable.
	const pageSize = 100
	for offset := 0; ; offset += pageSize {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		apiURL := fmt.Sprintf("%s/v1/collections?limit=%d&offset=%d", s.APIHost, pageSize, offset)
		resp, err := httpx.Do(ctx, s.Client, httpx.Request{
			URL: apiURL,
			Headers: map[string]string{
				"Instance": token,
				"Accept":   "application/json",
			},
		})
		if err != nil {
			return 0, fmt.Errorf("fetching collections: %w", err)
		}

		var result struct {
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
			Result []collection `json:"result"`
		}
		err = func() error {
			defer func() { _ = resp.Body.Close() }()
			return httpx.DecodeJSON(resp.Body, &result)
		}()
		if err != nil {
			return 0, fmt.Errorf("decoding collections: %w", err)
		}

		all = append(all, result.Result...)
		if len(result.Result) < pageSize || len(all) >= result.Meta.Total {
			break
		}
	}

	for _, c := range all {
		if slugify(c.Name) == slug {
			return c.ID, nil
		}
	}
	want := looseSlugKey(slug)
	for _, c := range all {
		if looseSlugKey(c.Name) == want {
			return c.ID, nil
		}
	}
	return 0, fmt.Errorf("collection %q not found", slug)
}

// looseSlugKey reduces a name or a slug to its letters and digits, so two
// spellings that differ only in how punctuation became a separator compare
// equal ("Mike's Apartment", "mike-s-apartment" and "mikes-apartment" all key
// to "mikesapartment").
func looseSlugKey(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return -1
	}, s)
}

func slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		if r == ' ' || r == '-' {
			return '-'
		}
		return -1
	}, s)
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

func (s *Scraper) FetchPage(ctx context.Context, token string, filter Filter, page int) ([]Release, int, error) {
	params := url.Values{}
	params.Set("type", "scene")
	params.Set("limit", strconv.Itoa(HitsPerPage))
	params.Set("offset", strconv.Itoa(page*HitsPerPage))
	// The API reads `orderBy`, not `orderby`, and takes a leading "-" for
	// descending. Spelled wrongly it is ignored and the order is arbitrary,
	// which makes the KnownIDs early-stop meaningless. Live-verified.
	params.Set("orderBy", "-dateReleased")

	switch filter.Type {
	case FilterActor:
		params.Set("actorId", strconv.Itoa(filter.ID))
	case FilterCollection:
		params.Set("collectionId", strconv.Itoa(filter.ID))
	case FilterTag:
		params.Set("tagId", strconv.Itoa(filter.ID))
	}

	apiURL := fmt.Sprintf("%s/v2/releases?%s", s.APIHost, params.Encode())

	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL: apiURL,
		Headers: map[string]string{
			"Instance": token,
			"Accept":   "application/json",
		},
	})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result ReleasesResponse
	if err := httpx.DecodeJSON(resp.Body, &result); err != nil {
		return nil, 0, fmt.Errorf("decoding releases: %w", err)
	}
	return result.Result, result.Meta.Total, nil
}

func (s *Scraper) fetchSeries(ctx context.Context, token string, seriesID int, delay time.Duration) ([]Release, int, error) {
	for offset := 0; ; offset += HitsPerPage {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		// A series can span many pages, and this walk used to issue them
		// back to back regardless of --delay.
		if offset > 0 && !scraper.Pace(ctx, delay) {
			return nil, 0, ctx.Err()
		}
		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, offset)
		params := url.Values{}
		params.Set("type", "serie")
		params.Set("limit", strconv.Itoa(HitsPerPage))
		params.Set("offset", strconv.Itoa(offset))

		apiURL := fmt.Sprintf("%s/v2/releases?%s", s.APIHost, params.Encode())

		resp, err := httpx.Do(ctx, s.Client, httpx.Request{
			URL: apiURL,
			Headers: map[string]string{
				"Instance": token,
				"Accept":   "application/json",
			},
		})
		if err != nil {
			return nil, 0, err
		}

		var result ReleasesResponse
		err = func() error {
			defer func() { _ = resp.Body.Close() }()
			return httpx.DecodeJSON(resp.Body, &result)
		}()
		if err != nil {
			return nil, 0, fmt.Errorf("decoding series: %w", err)
		}

		if len(result.Result) == 0 {
			return nil, 0, fmt.Errorf("series %d not found", seriesID)
		}

		for _, sr := range result.Result {
			if sr.ID != seriesID {
				continue
			}
			enriched := make([]Release, 0, len(sr.Children))
			for _, child := range sr.Children {
				if child.Type != "scene" {
					continue
				}
				if len(child.Actors) == 0 {
					child.Actors = sr.Actors
				}
				if child.DateReleased == "" {
					child.DateReleased = sr.DateReleased
				}
				if child.Description == "" {
					child.Description = sr.Description
				}
				if isEmptyJSON(child.RawImages) {
					child.RawImages = sr.RawImages
				}
				if len(child.Collections) == 0 {
					child.Collections = sr.Collections
				}
				if len(child.Tags) == 0 {
					child.Tags = sr.Tags
				}
				enriched = append(enriched, child)
			}
			return enriched, len(enriched), nil
		}

		if offset+HitsPerPage >= result.Meta.Total {
			return nil, 0, fmt.Errorf("series %d not found", seriesID)
		}
	}
}

func isEmptyJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("{}")) || bytes.Equal(trimmed, []byte("null"))
}

func (s *Scraper) Run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	token, err := s.FetchToken(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	filter := ParseFilter(studioURL)

	if filter.Slug != "" {
		id, err := s.resolveCollectionSlug(ctx, token, filter.Slug)
		if err != nil {
			select {
			case out <- scraper.Error(err):
			case <-ctx.Done():
			}
			return
		}
		filter.ID = id
		filter.Slug = ""
	}

	if filter.Type == FilterSeries {
		s.runSeries(ctx, studioURL, opts, out, token, filter.ID)
		return
	}

	for page := 0; ; page++ {
		if ctx.Err() != nil {
			return
		}

		if page > 0 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)

		releases, total, err := s.FetchPage(ctx, token, filter, page)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		if len(releases) == 0 {
			return
		}

		if page == 0 && total > 0 {
			scraper.Debugf(1, "%s: %d total scenes", s.cfg.SiteID, total)
			select {
			case out <- scraper.Progress(total):
			case <-ctx.Done():
				return
			}
		}

		now := time.Now().UTC()
		for _, rel := range releases {
			id := strconv.Itoa(rel.ID)
			if opts.KnownIDs[id] {
				scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}

			scene := ToScene(s.cfg, studioURL, rel, now)
			select {
			case out <- scraper.Scene(scene):
			case <-ctx.Done():
				return
			}
		}

		if (page+1)*HitsPerPage >= total {
			return
		}
	}
}

func (s *Scraper) runSeries(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, token string, seriesID int) {
	releases, total, err := s.fetchSeries(ctx, token, seriesID, opts.Delay)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	if total > 0 {
		scraper.Debugf(1, "%s: %d total scenes", s.cfg.SiteID, total)
		select {
		case out <- scraper.Progress(total):
		case <-ctx.Done():
			return
		}
	}

	// No early stop here: a series is episode-ordered, not date-ordered, so a
	// known id says nothing about what follows it.
	now := time.Now().UTC()
	for _, rel := range releases {
		scene := ToScene(s.cfg, studioURL, rel, now)
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}
}

func ToScene(cfg SiteConfig, studioURL string, rel Release, now time.Time) models.Scene {
	// Names are trimmed and blanks dropped: the API returns some with stray
	// whitespace ("Nikki Nuttz " on babes.com), and `fss stash import` looks
	// performers, tags and studios up in Stash by exact name — so an untrimmed
	// name creates a duplicate performer instead of matching the existing one.
	// match.MergeScenes trims as well, which covers catalogues already on disk;
	// this keeps newly written JSON clean at the source.
	performers := make([]string, 0, len(rel.Actors))
	for _, a := range rel.Actors {
		if name := strings.TrimSpace(a.Name); name != "" {
			performers = append(performers, name)
		}
	}

	tags := make([]string, 0, len(rel.Tags))
	for _, t := range rel.Tags {
		if name := strings.TrimSpace(t.Name); name != "" {
			tags = append(tags, name)
		}
	}

	var series string
	if len(rel.Collections) > 0 {
		series = strings.TrimSpace(rel.Collections[0].Name)
	}

	scenePath := cfg.ScenePath
	if scenePath == "" {
		scenePath = "video"
	}
	sceneURL := fmt.Sprintf("%s/%s/%d/%s", cfg.SiteBase, scenePath, rel.ID, Slugify(rel.Title))

	return models.Scene{
		ID:          strconv.Itoa(rel.ID),
		SiteID:      cfg.SiteID,
		StudioURL:   studioURL,
		Title:       rel.Title,
		URL:         sceneURL,
		Date:        ParseDate(rel.DateReleased),
		Description: rel.Description,
		Thumbnail:   ThumbnailURL(rel.RawImages),
		Preview:     PreviewURL(rel.RawVideos),
		Performers:  performers,
		Studio:      cfg.StudioName,
		Tags:        tags,
		Series:      series,
		Duration:    MediaDuration(rel.RawVideos),
		Likes:       rel.Stats.Likes,
		Views:       rel.Stats.Views,
		ScrapedAt:   now,
	}
}

func ParseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(title string) string {
	s := strings.ToLower(title)
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func ThumbnailURL(raw json.RawMessage) string {
	if isEmptyJSON(raw) {
		return ""
	}

	var images map[string]json.RawMessage
	if json.Unmarshal(raw, &images) != nil {
		warnParseFailure(&warnImagesOnce, "images", raw)
		return ""
	}

	posterRaw, ok := images["poster"]
	if !ok {
		return ""
	}

	var poster map[string]json.RawMessage
	if json.Unmarshal(posterRaw, &poster) != nil {
		warnParseFailure(&warnPosterOnce, "images.poster", posterRaw)
		return ""
	}

	variantRaw, ok := poster["0"]
	if !ok {
		return ""
	}

	var variant map[string]json.RawMessage
	if json.Unmarshal(variantRaw, &variant) != nil {
		warnParseFailure(&warnVariantOnce, "images.poster[0]", variantRaw)
		return ""
	}

	for _, size := range []string{"xx", "xl", "lg", "md", "sm", "xs"} {
		sizeRaw, ok := variant[size]
		if !ok {
			continue
		}
		var img struct {
			URLs struct {
				Default string `json:"default"`
			} `json:"urls"`
		}
		if json.Unmarshal(sizeRaw, &img) == nil && img.URLs.Default != "" {
			return img.URLs.Default
		}
	}
	return ""
}

func PreviewURL(raw json.RawMessage) string {
	if isEmptyJSON(raw) || bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		return ""
	}

	var videos struct {
		Mediabook *struct {
			Files map[string]struct {
				URLs struct {
					View string `json:"view"`
				} `json:"urls"`
			} `json:"files"`
		} `json:"mediabook"`
	}
	if err := json.Unmarshal(raw, &videos); err != nil {
		warnParseFailure(&warnVideosPreviewOnce, "videos (preview)", raw)
		return ""
	}
	if videos.Mediabook == nil {
		// Legitimate: not every release has a mediabook preview.
		return ""
	}

	best := 0
	var result string
	for format, f := range videos.Mediabook.Files {
		h := parseHeight(format)
		if h > best && f.URLs.View != "" {
			best = h
			result = f.URLs.View
		}
	}
	return result
}

func MediaDuration(raw json.RawMessage) int {
	if isEmptyJSON(raw) || bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		return 0
	}

	var videos struct {
		Mediabook *struct {
			Length int `json:"length"`
		} `json:"mediabook"`
	}
	if err := json.Unmarshal(raw, &videos); err != nil {
		warnParseFailure(&warnVideosDurationOnce, "videos (duration)", raw)
		return 0
	}
	if videos.Mediabook == nil {
		return 0
	}
	return videos.Mediabook.Length
}

func parseHeight(s string) int {
	s = strings.TrimSuffix(s, "p")
	n, _ := strconv.Atoi(s)
	return n
}

// One sync.Once per silent-unmarshal site so we surface schema breaks
// without spamming stderr (one warning per process per site, not per scene).
// If you add a new unmarshal site that should warn, add a new Once here.
var (
	warnImagesOnce         sync.Once
	warnPosterOnce         sync.Once
	warnVariantOnce        sync.Once
	warnVideosPreviewOnce  sync.Once
	warnVideosDurationOnce sync.Once
)

// warnParseFailure logs at most once per Once that an Aylo JSON payload
// failed to unmarshal. Includes a truncated sample so you can diff against
// the expected shape.
func warnParseFailure(once *sync.Once, where string, raw json.RawMessage) {
	once.Do(func() {
		const maxSample = 200
		sample := string(raw)
		if len(sample) > maxSample {
			sample = sample[:maxSample] + "...(truncated)"
		}
		fmt.Fprintf(os.Stderr,
			"warning: ayloutil: failed to parse %s JSON; affected fields will be missing for this and subsequent scenes. First failed payload: %s\n",
			where, sample)
	})
}
