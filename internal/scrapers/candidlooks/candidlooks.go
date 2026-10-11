// Package candidlooks scrapes the CandidLooks network sites (Amateur
// Lapdancer, OnlyFacials, Genuine Features). Each runs WordPress under
// /content/ and publishes its member-video teasers through the WP REST API.
package candidlooks

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const perPage = 100

// SiteConfig describes one network site.
type SiteConfig struct {
	ID   string
	Host string
	Name string
	// Base overrides https://{Host}; tests point it at an httptest server.
	Base string
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	if cfg.Base == "" {
		cfg.Base = "https://" + cfg.Host
	}
	return &Scraper{cfg: cfg, client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.ID }

func (s *Scraper) Patterns() []string {
	h := s.cfg.Host
	return []string{
		h,
		h + "/content/",
		h + "/content/category/{slug}/",
		h + "/content/tag/{slug}/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.cfg.matchRe().MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type rendered struct {
	Rendered string `json:"rendered"`
}

type wpPost struct {
	ID       int      `json:"id"`
	Date     string   `json:"date"`
	Link     string   `json:"link"`
	Title    rendered `json:"title"`
	Content  rendered `json:"content"`
	Embedded struct {
		Terms [][]struct {
			Taxonomy string `json:"taxonomy"`
			Name     string `json:"name"`
		} `json:"wp:term"`
	} `json:"_embedded"`
}

type wpTerm struct {
	ID int `json:"id"`
}

var filterRe = regexp.MustCompile(`(?i)/content/(category|tag)/([^/?#]+)`)

// filterParam turns a category/tag archive URL into the REST filter
// (e.g. "categories=6"); empty for the whole site.
func (s *Scraper) filterParam(ctx context.Context, studioURL string) (string, error) {
	m := filterRe.FindStringSubmatch(studioURL)
	if m == nil {
		return "", nil
	}
	endpoint := "categories"
	if strings.EqualFold(m[1], "tag") {
		endpoint = "tags"
	}
	var terms []wpTerm
	u := fmt.Sprintf("%s/content/wp-json/wp/v2/%s?slug=%s", s.cfg.Base, endpoint, url.QueryEscape(m[2]))
	if _, err := s.getJSON(ctx, u, &terms); err != nil {
		return "", err
	}
	if len(terms) == 0 {
		return "", fmt.Errorf("%s: no %s %q", s.cfg.ID, m[1], m[2])
	}
	return fmt.Sprintf("%s=%d", endpoint, terms[0].ID), nil
}

func (s *Scraper) getJSON(ctx context.Context, u string, v any) (http.Header, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := httpx.DecodeJSON(resp.Body, v); err != nil {
		return nil, scraper.ParseError(u, err)
	}
	return resp.Header, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	filter, err := s.filterParam(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	now := time.Now().UTC()
	scraper.Debugf(1, "%s: scraping %s via WP REST (filter %q)", s.cfg.ID, s.cfg.Base, filter)

	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/content/wp-json/wp/v2/posts?per_page=%d&page=%d&_embed=1", s.cfg.Base, perPage, page)
		if filter != "" {
			u += "&" + filter
		}
		var posts []wpPost
		hdr, err := s.getJSON(ctx, u, &posts)
		if err != nil {
			return scraper.PageResult{}, err
		}
		total := 0
		if page == 1 {
			total, _ = strconv.Atoi(strings.TrimSpace(hdr.Get("X-WP-Total")))
		}
		done := len(posts) < perPage
		if tp, err := strconv.Atoi(strings.TrimSpace(hdr.Get("X-WP-TotalPages"))); err == nil {
			done = page >= tp
		}
		scenes := make([]models.Scene, 0, len(posts))
		for _, p := range posts {
			if sc, ok := s.toScene(p, studioURL, now); ok {
				scenes = append(scenes, sc)
			}
		}
		return scraper.PageResult{Scenes: scenes, Total: total, Done: done}, nil
	})
}

var (
	videoMarkerRe = regexp.MustCompile(`class=['"](?:play_box|join_block)['"]`)
	posterImgRe   = regexp.MustCompile(`<img\b[^>]*\bclass="poster"[^>]*>`)
	srcRe         = regexp.MustCompile(`\bsrc="([^"]+)"`)
	tagRe         = regexp.MustCompile(`<[^>]+>`)
	spaceRe       = regexp.MustCompile(`\s+`)
)

// isVideo separates video teasers from the picture-set posts that share the
// category tree on Genuine Features.
func isVideo(content string) bool {
	return videoMarkerRe.MatchString(content) || posterImgRe.MatchString(content)
}

// description is the blurb that precedes the player block.
func description(content string) string {
	if i := strings.Index(content, "<div"); i >= 0 {
		content = content[:i]
	}
	return clean(tagRe.ReplaceAllString(content, " "))
}

func poster(content string) string {
	if m := posterImgRe.FindString(content); m != "" {
		if sm := srcRe.FindStringSubmatch(m); sm != nil {
			return sm[1]
		}
	}
	return ""
}

func clean(s string) string {
	return strings.TrimSpace(spaceRe.ReplaceAllString(html.UnescapeString(s), " "))
}

func (s *Scraper) toScene(p wpPost, studioURL string, now time.Time) (models.Scene, bool) {
	if !isVideo(p.Content.Rendered) {
		return models.Scene{}, false
	}
	sc := models.Scene{
		ID:          strconv.Itoa(p.ID),
		SiteID:      s.cfg.ID,
		StudioURL:   studioURL,
		Studio:      s.cfg.Name,
		Title:       clean(p.Title.Rendered),
		URL:         p.Link,
		Description: description(p.Content.Rendered),
		Thumbnail:   poster(p.Content.Rendered),
		ScrapedAt:   now,
	}
	if t, err := time.Parse("2006-01-02T15:04:05", strings.TrimSpace(p.Date)); err == nil {
		sc.Date = t.UTC()
	}
	seen := map[string]bool{}
	for _, group := range p.Embedded.Terms {
		for _, t := range group {
			name := clean(t.Name)
			if t.Taxonomy == "post_tag" && name != "" && !seen[strings.ToLower(name)] {
				seen[strings.ToLower(name)] = true
				sc.Tags = append(sc.Tags, name)
			}
		}
	}
	return sc, true
}
