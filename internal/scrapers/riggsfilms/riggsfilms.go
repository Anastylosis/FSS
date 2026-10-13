// Package riggsfilms scrapes riggsfilms.com, a WordPress teaser site whose
// scenes are posts in the "Videos" category. See docs/scrapers.md.
package riggsfilms

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
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
	siteID     = "riggsfilms"
	studioName = "Riggs Films"
	// videosSlug is the category every real scene carries; the other posts are
	// photo sets, model bios and a blog entry.
	videosSlug    = "videos"
	pageSize      = 50
	detailWorkers = 4
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?riggsfilms\.com(?:/|$)`)
	categoryRe = regexp.MustCompile(`^/category/([^/]+)/?$`)
	tagRe      = regexp.MustCompile(`^/tag/([^/]+)/?$`)
	modelRe    = regexp.MustCompile(`^/([^/]+)/?$`)

	ldJSONRe = regexp.MustCompile(`(?s)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	tagStrip = regexp.MustCompile(`<[^>]+>`)

	// notPerformer lists the categories that are content types or a series,
	// not people.
	notPerformer = map[string]bool{
		videosSlug: true, "photosets": true, "models": true, "blog-post": true,
		"portfolio": true, "uncategorised": true, "primal-instincts": true,
	}
)

// Scraper implements scraper.StudioScraper for riggsfilms.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Riggs Films scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://riggsfilms.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"riggsfilms.com",
		"riggsfilms.com/category/{category}/",
		"riggsfilms.com/tag/{tag}/",
		"riggsfilms.com/{model}/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type term struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type post struct {
	ID      int      `json:"id"`
	Date    string   `json:"date"`
	Link    string   `json:"link"`
	Title   rendered `json:"title"`
	Excerpt rendered `json:"excerpt"`
	Cats    []int    `json:"categories"`
	Tags    []int    `json:"tags"`
}

type rendered struct {
	Rendered string `json:"rendered"`
}

// filter is what an operator URL narrows the Videos category to.
type filter struct {
	category string
	tag      string
}

func parseFilter(studioURL string) filter {
	u, err := url.Parse(studioURL)
	if err != nil {
		return filter{}
	}
	p := u.Path
	switch {
	case categoryRe.MatchString(p):
		return filter{category: categoryRe.FindStringSubmatch(p)[1]}
	case tagRe.MatchString(p):
		return filter{tag: tagRe.FindStringSubmatch(p)[1]}
	case modelRe.MatchString(p):
		return filter{category: modelRe.FindStringSubmatch(p)[1]}
	}
	return filter{}
}

type taxonomy struct {
	catByID  map[int]term
	catBySlg map[string]term
	tagByID  map[int]term
	tagBySlg map[string]term
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	f := parseFilter(studioURL)
	scraper.Debugf(1, "%s: filter %+v", siteID, f)

	fail := func(err error) {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
	}

	tx, err := s.loadTaxonomy(ctx)
	if err != nil {
		fail(err)
		return
	}
	videos, ok := tx.catBySlg[videosSlug]
	if !ok {
		fail(scraper.ParseError(s.base, fmt.Errorf("no %q category", videosSlug)))
		return
	}

	query := url.Values{}
	query.Set("per_page", strconv.Itoa(pageSize))
	query.Set("_fields", "id,date,link,title,excerpt,categories,tags")
	query.Set("categories", strconv.Itoa(videos.ID))
	// WordPress ORs several values of one parameter, so a category filter is
	// applied client-side while a tag filter can ride along as an AND.
	wantCat := 0
	if f.category != "" {
		c, ok := tx.catBySlg[f.category]
		if !ok || notPerformer[c.Slug] && c.Slug != videosSlug {
			fail(fmt.Errorf("riggsfilms: unknown category %q", f.category))
			return
		}
		wantCat = c.ID
	}
	if f.tag != "" {
		t, ok := tx.tagBySlg[f.tag]
		if !ok {
			fail(fmt.Errorf("riggsfilms: unknown tag %q", f.tag))
			return
		}
		query.Set("tags", strconv.Itoa(t.ID))
	}

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		query.Set("page", strconv.Itoa(page))
		var posts []post
		hdr, err := s.getJSON(ctx, s.base+"/wp-json/wp/v2/posts?"+query.Encode(), &posts)
		if err != nil {
			return scraper.PageResult{}, err
		}
		total, _ := strconv.Atoi(hdr.Get("X-WP-Total"))
		pages, _ := strconv.Atoi(hdr.Get("X-WP-TotalPages"))

		var scenes []models.Scene
		for _, p := range posts {
			if wantCat != 0 && !hasInt(p.Cats, wantCat) {
				continue
			}
			scenes = append(scenes, buildScene(p, tx, studioURL, now))
		}
		enrichTo := len(scenes)
		for i, sc := range scenes {
			if opts.KnownIDs[sc.ID] {
				enrichTo = i
				break
			}
		}
		s.enrich(ctx, scenes[:enrichTo], opts, out)
		return scraper.PageResult{
			Scenes:   scenes,
			Total:    total,
			Done:     page >= pages,
			Continue: len(posts) > 0,
		}, nil
	})
}

func hasInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func buildScene(p post, tx taxonomy, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          strconv.Itoa(p.ID),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Studio:      studioName,
		Title:       cleanText(p.Title.Rendered),
		URL:         p.Link,
		Description: cleanText(p.Excerpt.Rendered),
		ScrapedAt:   now,
	}
	// The post date is the site's local calendar day; keep it as a UTC date.
	if t, err := parseutil.TryParseDate(p.Date, "2006-01-02T15:04:05"); err == nil {
		sc.Date = t.UTC()
	}
	for _, id := range p.Cats {
		c, ok := tx.catByID[id]
		if !ok {
			continue
		}
		if c.Slug == "primal-instincts" {
			sc.Series = html.UnescapeString(c.Name)
			continue
		}
		if !notPerformer[c.Slug] {
			sc.Performers = append(sc.Performers, html.UnescapeString(c.Name))
		}
	}
	for _, id := range p.Tags {
		if t, ok := tx.tagByID[id]; ok {
			sc.Tags = append(sc.Tags, html.UnescapeString(t.Name))
		}
	}
	return sc
}

// enrich reads each scene's runtime and poster from its page's VideoObject
// JSON-LD; the REST post carries neither.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(scenes) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range scenes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			v, err := parseVideoObject(body)
			if err != nil {
				select {
				case out <- scraper.Error(scraper.ParseError(scenes[i].URL, err)):
				case <-ctx.Done():
				}
				return
			}
			scenes[i].Duration = v.duration
			scenes[i].Thumbnail = v.thumbnail
		}(i)
	}
	wg.Wait()
}

type videoObject struct {
	duration  int
	thumbnail string
}

// parseVideoObject picks the first VideoObject block that has a runtime.
func parseVideoObject(body string) (videoObject, error) {
	for _, m := range ldJSONRe.FindAllStringSubmatch(body, -1) {
		var v struct {
			Type      string          `json:"@type"`
			Duration  string          `json:"duration"`
			Thumbnail json.RawMessage `json:"thumbnailUrl"`
		}
		if json.Unmarshal([]byte(m[1]), &v) != nil || v.Type != "VideoObject" {
			continue
		}
		d := parseutil.ParseDurationISO(v.Duration)
		if d == 0 {
			continue
		}
		return videoObject{duration: d, thumbnail: firstString(v.Thumbnail)}, nil
	}
	return videoObject{}, fmt.Errorf("no VideoObject with a duration")
}

// firstString reads a JSON string or the first element of a string array.
func firstString(raw json.RawMessage) string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil && len(many) > 0 {
		return many[0]
	}
	return ""
}

func (s *Scraper) loadTaxonomy(ctx context.Context) (taxonomy, error) {
	tx := taxonomy{
		catByID: map[int]term{}, catBySlg: map[string]term{},
		tagByID: map[int]term{}, tagBySlg: map[string]term{},
	}
	for _, kind := range []string{"categories", "tags"} {
		for page := 1; ; page++ {
			var terms []term
			hdr, err := s.getJSON(ctx, fmt.Sprintf("%s/wp-json/wp/v2/%s?per_page=100&page=%d&_fields=id,name,slug", s.base, kind, page), &terms)
			if err != nil {
				return tx, err
			}
			for _, t := range terms {
				if kind == "categories" {
					tx.catByID[t.ID], tx.catBySlg[t.Slug] = t, t
				} else {
					tx.tagByID[t.ID], tx.tagBySlg[t.Slug] = t, t
				}
			}
			if n, _ := strconv.Atoi(hdr.Get("X-WP-TotalPages")); page >= n {
				break
			}
		}
	}
	return tx, nil
}

func (s *Scraper) getJSON(ctx context.Context, u string, dst any) (http.Header, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return nil, scraper.ParseError(u, err)
	}
	return resp.Header, nil
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: pageURL, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStrip.ReplaceAllString(s, " "))), " ")
}
