package fycutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// RecommendedDelay is the pace this scraper was written against. It is **not**
// silently enforced — the operator's `opts.Delay` is always honoured, including
// `--delay 0` — but `WarnDelayBelow` surfaces a one-shot stderr warning below it.
const RecommendedDelay = 500 * time.Millisecond

type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	matchRe *regexp.Regexp
}

func New(cfg SiteConfig) *Scraper {
	re := regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`)
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		matchRe: re,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/?page={page}",
		s.cfg.Domain + "/models/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var modelPageRe = regexp.MustCompile(`/models/([a-z0-9-]+)`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.WarnDelayBelow(s.cfg.SiteID, opts.Delay, RecommendedDelay)

	isModel := modelPageRe.MatchString(studioURL)
	dataKey := "tourMainPageData"
	releasesKey := "latestReleases"
	if isModel {
		dataKey = "tourModelPageData"
		releasesKey = "releases"
	}

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		var pageURL string
		if isModel {
			pageURL = studioURL
			if page > 1 {
				if strings.Contains(pageURL, "?") {
					pageURL = fmt.Sprintf("%s&page=%d", pageURL, page)
				} else {
					pageURL = fmt.Sprintf("%s?page=%d", pageURL, page)
				}
			}
		} else {
			base := strings.TrimRight(studioURL, "/")
			pageURL = fmt.Sprintf("%s/?page=%d", base, page)
		}

		releases, hasNext, err := s.fetchPage(ctx, pageURL, dataKey, releasesKey)
		if err != nil {
			return scraper.PageResult{}, err
		}

		if len(releases) == 0 {
			return scraper.PageResult{}, nil
		}

		scenes := make([]models.Scene, len(releases))
		for i, rel := range releases {
			scenes[i] = s.toScene(rel, studioURL)
		}
		return scraper.PageResult{Scenes: scenes, Done: !hasNext}, nil
	})
}

var nuxtDataRe = regexp.MustCompile(`<script[^>]*id="__NUXT_DATA__"[^>]*>(.*?)</script>`)

func (s *Scraper) fetchPage(ctx context.Context, pageURL, dataKey, releasesKey string) ([]map[string]any, bool, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, false, err
	}

	m := nuxtDataRe.FindSubmatch(body)
	if m == nil {
		return nil, false, fmt.Errorf("no __NUXT_DATA__ found in %s", pageURL)
	}

	var flat []any
	if err := json.Unmarshal(m[1], &flat); err != nil {
		return nil, false, fmt.Errorf("parsing nuxt data: %w", err)
	}

	r := &resolver{data: flat, memo: make(map[int]any, len(flat))}

	// Navigate: root → {data: N} → {tourMainPageData: N} → {latestReleases: N} → {items: [indices], pagination: N}
	section, ok := r.walkPath(0, "data", dataKey, releasesKey)
	if !ok {
		return nil, false, nil
	}

	sectionMap, ok := section.(map[string]any)
	if !ok {
		return nil, false, nil
	}

	itemsRaw, _ := sectionMap["items"].([]any)
	if len(itemsRaw) == 0 {
		return nil, false, nil
	}

	var releases []map[string]any
	for _, item := range itemsRaw {
		if rel, ok := item.(map[string]any); ok {
			releases = append(releases, rel)
		}
	}

	hasNext := true
	if pag, ok := sectionMap["pagination"].(map[string]any); ok {
		if pag["nextPage"] == nil {
			hasNext = false
		}
	}

	return releases, hasNext, nil
}

func (s *Scraper) toScene(rel map[string]any, studioURL string) models.Scene {
	id := fmtID(rel["id"])
	slug, _ := rel["cachedSlug"].(string)
	title, _ := rel["title"].(string)
	description, _ := rel["description"].(string)
	description = strings.TrimSpace(description)

	var date time.Time
	if dateStr, ok := rel["releasedAt"].(string); ok && dateStr != "" {
		date, _ = time.Parse(time.RFC3339, dateStr)
	}

	var performers []string
	if actors, ok := rel["actors"].([]any); ok {
		for _, a := range actors {
			if actor, ok := a.(map[string]any); ok {
				if name, ok := actor["name"].(string); ok {
					performers = append(performers, name)
				}
			}
		}
	}

	var tags []string
	if rawTags, ok := rel["tags"].([]any); ok {
		for _, t := range rawTags {
			if tag, ok := t.(string); ok {
				tags = append(tags, tag)
			}
		}
	}

	thumb, _ := rel["thumbUrl"].(string)
	if poster, ok := rel["posterUrl"].(string); ok && poster != "" {
		thumb = poster
	}

	sceneURL := fmt.Sprintf("https://%s/video/%s", s.cfg.Domain, slug)

	return models.Scene{
		ID:          id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Title:       title,
		URL:         sceneURL,
		Date:        date.UTC(),
		Description: description,
		Thumbnail:   thumb,
		Performers:  performers,
		Studio:      s.cfg.StudioName,
		Tags:        tags,
		ScrapedAt:   time.Now().UTC(),
	}
}

func fmtID(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case string:
		return n
	default:
		return fmt.Sprint(v)
	}
}

// resolver handles Nuxt 3's devalue/dehydration format: a flat JSON array where
// object values and array elements are integer indices referencing other positions.
type resolver struct {
	data []any
	memo map[int]any
}

func (r *resolver) resolve(idx int) any {
	if idx < 0 || idx >= len(r.data) {
		return nil
	}
	if v, ok := r.memo[idx]; ok {
		return v
	}
	r.memo[idx] = nil

	val := r.data[idx]
	var result any

	switch v := val.(type) {
	case []any:
		if marker, ref, ok := specialArray(v); ok {
			switch marker {
			case "ShallowReactive", "Reactive", "Ref":
				result = r.resolve(ref)
				r.memo[idx] = result
				return result
			}
		}
		arr := make([]any, len(v))
		for i, elem := range v {
			if ref, ok := toInt(elem); ok {
				arr[i] = r.resolve(ref)
			} else {
				arr[i] = elem
			}
		}
		result = arr
	case map[string]any:
		obj := make(map[string]any, len(v))
		for key, val := range v {
			if ref, ok := toInt(val); ok {
				obj[key] = r.resolve(ref)
			} else {
				obj[key] = val
			}
		}
		result = obj
	default:
		result = val
	}

	r.memo[idx] = result
	return result
}

func (r *resolver) walkPath(start int, keys ...string) (any, bool) {
	current := r.resolve(start)
	for _, key := range keys {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		val, exists := obj[key]
		if !exists {
			return nil, false
		}
		current = val
	}
	return current, true
}

func specialArray(v []any) (string, int, bool) {
	if len(v) == 2 {
		if marker, ok := v[0].(string); ok {
			if ref, ok := toInt(v[1]); ok {
				return marker, ref, true
			}
		}
	}
	return "", 0, false
}

func toInt(v any) (int, bool) {
	if f, ok := v.(float64); ok {
		return int(f), true
	}
	return 0, false
}
