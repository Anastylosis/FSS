// Package bentleyrace scrapes bentleyrace.com through the WordPress REST API.
// See docs/scrapers.md.
package bentleyrace

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "bentleyrace"
	studioName = "Bentley Race"
	liveBase   = "https://bentleyrace.com"
	// videosCategory is the "Videos" category; its children are the
	// duo/solo/group/outdoors/pissing video categories.
	videosCategory = 10
	// perPage is the REST maximum.
	perPage = 100
	// postFields trims the REST payload to what buildScene reads; _links and
	// _embedded must be named or _embed is dropped.
	postFields = "id,date,link,title,content,class_list,yoast_head_json.og_image,_links.wp:term,_embedded"
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?bentleyrace\.com(?:[/?#]|$)`)

	modelPathRe    = regexp.MustCompile(`^/model/([^/]+)`)
	categoryPathRe = regexp.MustCompile(`^/category/(?:[^/]+/)*([^/]+)`)
	tagPathRe      = regexp.MustCompile(`^/tag/([^/]+)`)

	articleRe = regexp.MustCompile(`<article class="([^"]*)"`)
	postIDRe  = regexp.MustCompile(`\bpost-(\d+)\b`)
	nextRe    = regexp.MustCompile(`<link rel="next"`)
	htmlTagRe = regexp.MustCompile(`<[^>]+>`)
)

// Scraper implements scraper.StudioScraper for bentleyrace.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Bentley Race scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(60 * time.Second), base: liveBase}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"bentleyrace.com",
		"bentleyrace.com/model/{slug}",
		"bentleyrace.com/category/videos/{slug}",
		"bentleyrace.com/tag/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type term struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Taxonomy string `json:"taxonomy"`
	Parent   int    `json:"parent"`
}

type post struct {
	ID    int    `json:"id"`
	Date  string `json:"date"`
	Link  string `json:"link"`
	Title struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Content struct {
		Rendered string `json:"rendered"`
	} `json:"content"`
	ClassList []string `json:"class_list"`
	Yoast     struct {
		OGImage []struct {
			URL string `json:"url"`
		} `json:"og_image"`
	} `json:"yoast_head_json"`
	Embedded struct {
		Terms [][]term `json:"wp:term"`
	} `json:"_embedded"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	u, err := url.Parse(studioURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	p := strings.TrimSuffix(u.Path, "/")

	if m := modelPathRe.FindStringSubmatch(p); m != nil {
		scraper.Debugf(1, "%s: scraping model page %s", siteID, m[1])
		s.runModel(ctx, studioURL, m[1], opts, out)
		return
	}

	filter := fmt.Sprintf("categories=%d", videosCategory)
	switch {
	case categoryPathRe.MatchString(p):
		slug := categoryPathRe.FindStringSubmatch(p)[1]
		scraper.Debugf(1, "%s: scraping category %s", siteID, slug)
		id, err := s.videoCategory(ctx, slug)
		if err != nil {
			send(ctx, out, scraper.Error(err))
			return
		}
		filter = fmt.Sprintf("categories=%d", id)
	case tagPathRe.MatchString(p):
		slug := tagPathRe.FindStringSubmatch(p)[1]
		scraper.Debugf(1, "%s: scraping tag %s", siteID, slug)
		id, err := s.lookupTerm(ctx, "tags", slug)
		if err != nil {
			send(ctx, out, scraper.Error(err))
			return
		}
		filter += fmt.Sprintf("&tags=%d", id.ID)
	}

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/wp-json/wp/v2/posts?%s&per_page=%d&page=%d&_embed=wp:term&_fields=%s",
			s.base, filter, perPage, page, postFields)
		var posts []post
		h, err := s.apiGet(ctx, u, &posts)
		if err != nil {
			return scraper.PageResult{}, err
		}
		total, _ := strconv.Atoi(h.Get("X-WP-Total"))
		pages, _ := strconv.Atoi(h.Get("X-WP-TotalPages"))
		scenes := make([]models.Scene, 0, len(posts))
		for _, p := range posts {
			scenes = append(scenes, s.buildScene(p, studioURL, now))
		}
		done := len(posts) < perPage
		if pages > 0 {
			done = page >= pages
		}
		return scraper.PageResult{Scenes: scenes, Total: total, Done: done}, nil
	})
}

// runModel walks the model's HTML archive, which is the only place the
// model-name taxonomy is queryable (it is not exposed over REST), keeps the
// video posts and loads them over REST by id.
func (s *Scraper) runModel(ctx context.Context, studioURL, slug string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/model/%s", s.base, slug)
		if page > 1 {
			pageURL += fmt.Sprintf("/page/%d", page)
		}
		body, err := s.get(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		ids, articles := parseModelPage(body)
		if page == 1 && articles == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no posts on model page"))
		}
		done := !nextRe.Match(body)
		if len(ids) == 0 {
			return scraper.PageResult{Continue: !done, Done: done}, nil
		}
		u := fmt.Sprintf("%s/wp-json/wp/v2/posts?include=%s&orderby=include&per_page=%d&_embed=wp:term&_fields=%s",
			s.base, strings.Join(ids, ","), len(ids), postFields)
		var posts []post
		if _, err := s.apiGet(ctx, u, &posts); err != nil {
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, 0, len(posts))
		for _, p := range posts {
			scenes = append(scenes, s.buildScene(p, studioURL, now))
		}
		return scraper.PageResult{Scenes: scenes, Continue: !done, Done: done}, nil
	})
}

// parseModelPage returns the ids of the video posts on a model archive page and
// the number of posts of any kind (photo shoots and behind-the-scenes posts
// share the archive).
func parseModelPage(body []byte) ([]string, int) {
	var ids []string
	matches := articleRe.FindAllSubmatch(body, -1)
	for _, m := range matches {
		class := " " + string(m[1]) + " "
		if !strings.Contains(class, " category-videos ") {
			continue
		}
		if id := postIDRe.FindSubmatch(m[1]); id != nil {
			ids = append(ids, string(id[1]))
		}
	}
	return ids, len(matches)
}

// videoCategory resolves a category slug and refuses one outside Videos, since
// the photo-gallery and behind-the-scenes categories hold no scenes.
func (s *Scraper) videoCategory(ctx context.Context, slug string) (int, error) {
	t, err := s.lookupTerm(ctx, "categories", slug)
	if err != nil {
		return 0, err
	}
	if t.ID != videosCategory && t.Parent != videosCategory {
		return 0, fmt.Errorf("%s: category %q is not a video category", siteID, slug)
	}
	return t.ID, nil
}

func (s *Scraper) lookupTerm(ctx context.Context, taxonomy, slug string) (term, error) {
	u := fmt.Sprintf("%s/wp-json/wp/v2/%s?slug=%s&_fields=id,name,slug,parent", s.base, taxonomy, url.QueryEscape(slug))
	var terms []term
	if _, err := s.apiGet(ctx, u, &terms); err != nil {
		return term{}, err
	}
	if len(terms) == 0 {
		return term{}, fmt.Errorf("%s: no %s named %q", siteID, taxonomy, slug)
	}
	return terms[0], nil
}

func (s *Scraper) buildScene(p post, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          strconv.Itoa(p.ID),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       cleanText(p.Title.Rendered),
		URL:         absURL(s.base, p.Link),
		Studio:      studioName,
		Description: cleanText(p.Content.Rendered),
		ScrapedAt:   now,
	}
	if t, err := time.Parse("2006-01-02T15:04:05", p.Date); err == nil {
		// The site's local date, as the page displays it.
		sc.Date = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
	if len(p.Yoast.OGImage) > 0 {
		sc.Thumbnail = cleanImageURL(s.base, p.Yoast.OGImage[0].URL)
	}

	tagNames := map[string]string{}
	var tags []term
	var categories []string
	for _, group := range p.Embedded.Terms {
		for _, t := range group {
			switch t.Taxonomy {
			case "post_tag":
				tagNames[t.Slug] = cleanText(t.Name)
				tags = append(tags, t)
			case "category":
				if t.ID != videosCategory {
					categories = append(categories, cleanText(t.Name))
				}
			}
		}
	}

	// The cast is a model-name taxonomy that REST does not expose; the post's
	// class_list still carries its slugs, and each model also has a tag of the
	// same slug holding the display name.
	performerSlugs := map[string]bool{}
	for _, c := range p.ClassList {
		slug, ok := strings.CutPrefix(c, "model-name-")
		if !ok || slug == "" {
			continue
		}
		performerSlugs[slug] = true
		name := tagNames[slug]
		if name == "" {
			name = humanize(slug)
		}
		sc.Performers = append(sc.Performers, name)
	}
	for _, t := range tags {
		if !performerSlugs[t.Slug] {
			sc.Tags = append(sc.Tags, cleanText(t.Name))
		}
	}
	sc.Categories = categories
	return sc
}

func (s *Scraper) apiGet(ctx context.Context, rawURL string, v any) (http.Header, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, s.base),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := httpx.DecodeJSON(resp.Body, v); err != nil {
		return nil, scraper.ParseError(rawURL, err)
	}
	return resp.Header, nil
}

func (s *Scraper) get(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) {
	select {
	case out <- r:
	case <-ctx.Done():
	}
}

// absURL resolves the site's root-relative post links.
func absURL(base, ref string) string {
	if strings.HasPrefix(ref, "/") && !strings.HasPrefix(ref, "//") {
		return base + ref
	}
	return ref
}

// cleanImageURL resolves the "/wp/../public_images/" paths the site writes
// for its images.
func cleanImageURL(base, ref string) string {
	u, err := url.Parse(absURL(base, ref))
	if err != nil || u.Path == "" {
		return ref
	}
	u.Path = path.Clean(u.Path)
	return u.String()
}

func humanize(slug string) string {
	words := strings.Fields(strings.ReplaceAll(slug, "-", " "))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func cleanText(s string) string {
	s = html.UnescapeString(htmlTagRe.ReplaceAllString(s, " "))
	return strings.Join(strings.Fields(s), " ")
}
