package loyalfans

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	defaultBase = "https://www.loyalfans.com"
	perPage     = 20
	dateFormat  = "2006-01-02 15:04:05"
)

type Scraper struct {
	client *http.Client
	base   string // API base URL, overridable for tests.
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "loyalfans" }

func (s *Scraper) Patterns() []string {
	return []string{
		"loyalfans.com/{creator_slug}",
		"loyalfans.com/{creator_slug}/store",
	}
}

// The creator's store page is the URL StashDB records and the one a user is
// most likely to paste, so it is matched alongside the bare profile. Without
// the explicit /store branch slugFromURL's last-segment fallback would return
// "store" and scrape a creator that does not exist.
var matchRe = regexp.MustCompile(`^https?://(?:www\.)?loyalfans\.com/([a-zA-Z0-9_-]+)(?:/store)?/?$`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	slug := slugFromURL(studioURL)
	if slug == "" {
		return nil, fmt.Errorf("loyalfans: cannot extract creator slug from %q", studioURL)
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, slug, opts, out)
	return out, nil
}

func slugFromURL(u string) string {
	if m := matchRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	// Fallback: last non-empty path segment (supports test server URLs).
	parts := strings.Split(strings.TrimRight(u, "/"), "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

func (s *Scraper) run(ctx context.Context, studioURL, slug string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	cookies, err := s.initSession(ctx)
	if err != nil {
		send(ctx, out, scraper.Error(fmt.Errorf("loyalfans session: %w", err)))
		return
	}

	// The cursor listing is not date-ordered: bettie_bondage's 113 videos come
	// back with 60 descending violations (2023-10, 2023-09, 2023-11, 2024-02,
	// 2024-01, …), and the API exposes no sort to ask for. A KnownIDs stop would
	// therefore halt on whichever stored scene happened to surface first and
	// hide everything behind it, so every run re-walks. That is cheap here —
	// the pages carry the whole scene and there is no detail fetch behind them.
	opts.KnownIDs = nil

	var pageToken string

	// The cursor API can hand back a video already returned on an earlier page —
	// a saved catalogue showed 187 of 493 videos emitted twice, differing only
	// in the moment they were scraped. Whether the window shifts underneath the
	// token or the pages overlap by design, re-sending a scene inflates the live
	// progress count and repeats downstream work. The cmd layer drops duplicates
	// before saving, but it should not have to.
	seen := make(map[string]bool)
	duplicates := 0

	for {
		results, nextToken, err := s.fetchPage(ctx, slug, pageToken, cookies)
		if err != nil {
			send(ctx, out, scraper.Error(err))
			return
		}

		for _, v := range results {
			if v.Owner.Slug != slug {
				continue
			}
			scene := toScene(studioURL, slug, v)
			if seen[scene.ID] {
				duplicates++
				continue
			}
			seen[scene.ID] = true
			if !send(ctx, out, scraper.Scene(scene)) {
				return
			}
		}

		if nextToken == "" || len(results) < perPage {
			if duplicates > 0 {
				scraper.Debugf(1, "loyalfans: skipped %d video(s) the API returned more than once", duplicates)
			}
			break
		}
		pageToken = nextToken

		if !scraper.Pace(ctx, opts.Delay) {
			return
		}
	}
}

func (s *Scraper) initSession(ctx context.Context) ([]*http.Cookie, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		Method: http.MethodPost,
		URL:    s.base + "/api/v2/system-status",
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentChrome)
			h["Accept"] = "application/json"
			h["Content-Type"] = "application/json"
			h["Origin"] = s.base
			return h
		}(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.Cookies(), nil
}

type searchRequest struct {
	Query     string `json:"q"`
	Type      string `json:"type"`
	Limit     int    `json:"limit"`
	PageToken string `json:"page_token,omitempty"`
}

type searchResponse struct {
	Success   bool    `json:"success"`
	Data      []video `json:"data"`
	PageToken *string `json:"page_token"`
}

type video struct {
	UID   string `json:"uid"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Owner struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
	} `json:"owner"`
	Content   string `json:"content"`
	CreatedAt struct {
		Date string `json:"date"`
	} `json:"created_at"`
	VideoObject struct {
		Poster   string `json:"poster"`
		Duration int    `json:"duration"`
	} `json:"video_object"`
	Reactions struct {
		Total int `json:"total"`
	} `json:"reactions"`
	Hashtags []string `json:"hashtags"`
}

func cookieHeader(cookies []*http.Cookie) string {
	parts := make([]string, len(cookies))
	for i, c := range cookies {
		parts[i] = c.Name + "=" + c.Value
	}
	return strings.Join(parts, "; ")
}

func (s *Scraper) fetchPage(ctx context.Context, slug, pageToken string, cookies []*http.Cookie) ([]video, string, error) {
	body := searchRequest{
		Query: slug,
		Type:  "videos",
		Limit: perPage,
	}
	if pageToken != "" {
		body.PageToken = pageToken
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, "", err
	}

	headers := httpx.BrowserHeaders(httpx.UserAgentChrome)
	headers["Accept"] = "application/json"
	headers["Content-Type"] = "application/json"
	headers["Origin"] = s.base
	headers["Referer"] = s.base + "/" + slug
	if len(cookies) > 0 {
		headers["Cookie"] = cookieHeader(cookies)
	}

	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     s.base + "/api/v2/advanced-search?ngsw-bypass=true",
		Body:    bodyJSON,
		Headers: headers,
	})
	if err != nil {
		return nil, "", fmt.Errorf("loyalfans search page: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result searchResponse
	if err := httpx.DecodeJSON(resp.Body, &result); err != nil {
		return nil, "", fmt.Errorf("decoding search response: %w", err)
	}
	if !result.Success {
		return nil, "", fmt.Errorf("loyalfans search failed for %q", slug)
	}

	nextToken := ""
	if result.PageToken != nil {
		nextToken = *result.PageToken
	}
	return result.Data, nextToken, nil
}

func toScene(studioURL, creatorSlug string, v video) models.Scene {
	now := time.Now().UTC()
	sc := models.Scene{
		ID:        v.Slug,
		SiteID:    "loyalfans",
		StudioURL: studioURL,
		Title:     v.Title,
		URL:       defaultBase + "/" + creatorSlug + "/video/" + v.Slug,
		Duration:  v.VideoObject.Duration,
		Thumbnail: v.VideoObject.Poster,
		Studio:    v.Owner.DisplayName,
		Likes:     v.Reactions.Total,
		ScrapedAt: now,
	}

	if v.Content != "" {
		desc := html.UnescapeString(v.Content)
		desc = strings.ReplaceAll(desc, "<br />", "\n")
		desc = strings.ReplaceAll(desc, "<br>", "\n")
		desc = stripHashtags(desc)
		sc.Description = strings.TrimSpace(desc)
	}

	if v.CreatedAt.Date != "" {
		if t, err := time.Parse(dateFormat, v.CreatedAt.Date); err == nil {
			sc.Date = t.UTC()
		}
	}

	if v.Owner.DisplayName != "" {
		sc.Performers = []string{v.Owner.DisplayName}
	}

	for _, tag := range v.Hashtags {
		tag = strings.Trim(tag, "#. ")
		if tag != "" {
			sc.Tags = append(sc.Tags, tag)
		}
	}

	return sc
}

var hashtagRe = regexp.MustCompile(`#\w+`)

func stripHashtags(s string) string {
	return hashtagRe.ReplaceAllString(s, "")
}

func send(ctx context.Context, ch chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case ch <- r:
		return true
	case <-ctx.Done():
		return false
	}
}
