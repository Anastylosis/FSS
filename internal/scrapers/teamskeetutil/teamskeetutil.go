package teamskeetutil

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	esBase   = "https://tours-store.psmcdn.net"
	pageSize = 30
)

type SiteConfig struct {
	SiteID    string
	Domain    string
	SiteBase  string
	Index     string // ES index name (e.g. "ts_network", "mylf_bundle")
	ScenePath string // URL path prefix for scenes (default: "/videos/")
	// NickName is the site's `site.nickName` value in the shared ES index. When
	// set, a bare sub-site domain (no /models|/series|/categories path) filters
	// the shared index to just this site instead of scraping the whole network.
	// Leave empty for the network-hub configs that intentionally cover the whole
	// index (e.g. teamskeet, mylf).
	NickName string
}

type Scraper struct {
	client    *http.Client
	cfg       SiteConfig
	esBaseURL string // override for testing; defaults to esBase
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		cfg:    cfg,
	}
}
func (s *Scraper) Run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	kind, value := classifyURL(studioURL)
	// A bare sub-site domain classifies as filterAll, which would scrape the
	// entire shared ES index (the whole network). Constrain it to the
	// configured site via its nickName so it returns only that site's scenes.
	if kind == filterAll && s.cfg.NickName != "" {
		scraper.Debugf(1, "%s: bare domain, filtering to nickName %q", s.cfg.SiteID, s.cfg.NickName)
		kind, value = filterSeries, s.cfg.NickName
	}
	baseQuery := buildQuery(kind, value)
	baseQuery["size"] = pageSize
	now := time.Now().UTC()

	var searchAfter []json.RawMessage
	for page := 0; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 0 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)

		if searchAfter != nil {
			baseQuery["search_after"] = searchAfter
		}

		result, err := s.search(ctx, baseQuery)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		// A "gte" relation means Elasticsearch hit its count ceiling, so the
		// number is a floor and reporting it would have progress claim the
		// scrape finished at 10,000 of a larger catalogue. An unknown total is
		// the honest answer there.
		if page == 0 && result.Hits.Total.Value > 0 {
			if result.Hits.Total.Relation == "gte" {
				scraper.Debugf(1, "%s: total is a floor (>=%d), reporting it as unknown",
					s.cfg.SiteID, result.Hits.Total.Value)
			} else {
				select {
				case out <- scraper.Progress(result.Hits.Total.Value):
				case <-ctx.Done():
					return
				}
			}
		}

		if len(result.Hits.Hits) == 0 {
			return
		}

		for _, hit := range result.Hits.Hits {
			id := strconv.Itoa(hit.Source.ItemID)

			if opts.KnownIDs[id] {
				scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}

			scene := hitToScene(hit.Source, studioURL, s.cfg, now)
			select {
			case out <- scraper.Scene(scene):
			case <-ctx.Done():
				return
			}
		}

		lastHit := result.Hits.Hits[len(result.Hits.Hits)-1]
		if len(lastHit.Sort) == 0 {
			return
		}
		searchAfter = lastHit.Sort
	}
}

type filterKind int

const (
	filterAll filterKind = iota
	filterModel
	filterSeries
	filterCategory
)

var (
	modelURLRe    = regexp.MustCompile(`/models/([^/?#]+)`)
	seriesURLRe   = regexp.MustCompile(`/series/([^/?#]+)`)
	categoryURLRe = regexp.MustCompile(`/categories/([^/?#]+)`)
)

func classifyURL(u string) (filterKind, string) {
	if m := modelURLRe.FindStringSubmatch(u); m != nil {
		return filterModel, m[1]
	}
	if m := seriesURLRe.FindStringSubmatch(u); m != nil {
		return filterSeries, m[1]
	}
	if m := categoryURLRe.FindStringSubmatch(u); m != nil {
		return filterCategory, m[1]
	}
	return filterAll, ""
}

func buildQuery(kind filterKind, value string) map[string]any {
	must := []any{
		map[string]any{"term": map[string]any{"_doc_type.keyword": "tour_movie"}},
		map[string]any{"term": map[string]any{"isUpcoming": false}},
	}

	switch kind {
	case filterModel:
		must = append(must, map[string]any{"term": map[string]any{"models.id.keyword": value}})
	case filterSeries:
		must = append(must, map[string]any{"term": map[string]any{"site.nickName.keyword": value}})
	case filterCategory:
		tag := strings.ReplaceAll(value, "-", " ")
		must = append(must, map[string]any{"match_phrase": map[string]any{"tags": tag}})
	}

	return map[string]any{
		"query": map[string]any{
			"bool": map[string]any{"must": must},
		},
		"sort": []any{
			map[string]any{"publishedDate": map[string]any{"order": "desc"}},
			map[string]any{"_id": map[string]any{"order": "desc"}},
		},
	}
}

func (s *Scraper) search(ctx context.Context, query map[string]any) (*esResponse, error) {
	base := s.esBaseURL
	if base == "" {
		base = esBase
	}
	url := base + "/" + s.cfg.Index + "/_search"
	return s.searchWithURL(ctx, url, query)
}

func (s *Scraper) searchWithURL(ctx context.Context, url string, query map[string]any) (*esResponse, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:    url,
		Method: http.MethodPost,
		Body:   body,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Content-Type"] = "application/json"
			h["Origin"] = s.cfg.SiteBase
			return h
		}(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result esResponse
	if err := httpx.DecodeJSON(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

type esResponse struct {
	Hits struct {
		Total struct {
			Value int `json:"value"`
			// Relation is "eq" when Value is the real count and "gte" when it
			// is only a floor — Elasticsearch stops counting at 10,000 by
			// default, and the network's larger catalogues are past that.
			Relation string `json:"relation"`
		} `json:"total"`
		Hits []esHit `json:"hits"`
	} `json:"hits"`
}

type esHit struct {
	Source esScene           `json:"_source"`
	Sort   []json.RawMessage `json:"sort"`
}

type esScene struct {
	ID            string    `json:"id"`
	ItemID        int       `json:"itemId"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	Img           string    `json:"img"`
	VideoTrailer  string    `json:"videoTrailer"`
	PublishedDate string    `json:"publishedDate"`
	Tags          []string  `json:"tags"`
	Models        []esModel `json:"models"`
	Site          struct {
		Name     string `json:"name"`
		NickName string `json:"nickName"`
	} `json:"site"`
}

type esModel struct {
	ID     string `json:"id"`
	ItemID int    `json:"itemId"`
	Name   string `json:"name"`
}

var stripTagsRe = regexp.MustCompile(`<[^>]+>`)

func hitToScene(src esScene, studioURL string, cfg SiteConfig, now time.Time) models.Scene {
	id := strconv.Itoa(src.ItemID)

	var performers []string
	for _, m := range src.Models {
		if m.Name != "" {
			performers = append(performers, m.Name)
		}
	}

	desc := src.Description
	desc = stripTagsRe.ReplaceAllString(desc, "")
	desc = html.UnescapeString(desc)
	desc = strings.TrimSpace(desc)

	scenePath := cfg.ScenePath
	if scenePath == "" {
		scenePath = "/videos/"
	}

	scene := models.Scene{
		ID:          id,
		SiteID:      src.Site.NickName,
		StudioURL:   studioURL,
		Title:       src.Title,
		URL:         cfg.SiteBase + scenePath + src.ID,
		Thumbnail:   src.Img,
		Preview:     src.VideoTrailer,
		Description: desc,
		Performers:  performers,
		Tags:        src.Tags,
		Date:        parseDate(src.PublishedDate),
		Studio:      src.Site.Name,
		ScrapedAt:   now,
	}
	return scene
}

func parseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02T15:04:05", s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
