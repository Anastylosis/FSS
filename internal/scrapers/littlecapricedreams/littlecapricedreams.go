package littlecapricedreams

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID      = "littlecapricedreams"
	studioName  = "Little Caprice Dreams"
	defaultBase = "https://www.littlecaprice-dreams.com"
	pageSize    = 100
	// videosSlug is the category every video carries. It is a content-type
	// marker, not a brand: the sibling `gallery` holds the site's 344 photo
	// sets, which share the `project` post type and would otherwise be scraped
	// as scenes.
	videosSlug = "videos"
	// aliasHost is the Pornlifestyle sub-brand's own domain. It 301s every
	// path — the REST routes included — to the collection page below, so it
	// can be addressed but never fetched from.
	aliasHost       = "porn-lifestyle.com"
	aliasCollection = "porn-lifestyle"
)

// typeSlugs are the project_category terms that classify a post rather than
// name a sub-brand, and so never become Scene.Studio.
var typeSlugs = map[string]bool{
	videosSlug: true,
	"gallery":  true,
	"sticky":   true,
	"bonus":    true,
	"unlisted": true,
}

type Scraper struct {
	Client       *http.Client
	baseOverride string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(60 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"littlecaprice-dreams.com/{series}/",
		"littlecaprice-dreams.com",
		"littlecaprice-dreams.com/videos/",
		"littlecaprice-dreams.com/collection/{slug}/",
		"littlecaprice-dreams.com/model/{slug}/",
		"porn-lifestyle.com",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:littlecaprice-dreams\.com|porn-lifestyle\.com)(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) base(studioURL string) string {
	if s.baseOverride != "" {
		return strings.TrimSuffix(s.baseOverride, "/")
	}
	if u, err := url.Parse(studioURL); err == nil && u.Host != "" && !isAliasHost(u.Host) {
		return u.Scheme + "://" + u.Host
	}
	return defaultBase
}

func isAliasHost(host string) bool {
	return strings.EqualFold(strings.TrimPrefix(strings.ToLower(host), "www."), aliasHost)
}

var (
	modelURLRe      = regexp.MustCompile(`/model/([^/?#]+)`)
	collectionURLRe = regexp.MustCompile(`/collection/([^/?#]+)`)
	firstSegmentRe  = regexp.MustCompile(`^https?://[^/]+/([^/?#]+)/?$`)
	projectSlugRe   = regexp.MustCompile(`/project/([a-z0-9-]+)`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := s.base(studioURL)

	cats, err := s.fetchCategories(ctx, base)
	if err != nil {
		send(ctx, out, scraper.Error(fmt.Errorf("listing collections: %w", err)))
		return
	}
	videos, ok := cats.bySlug[videosSlug]
	if !ok {
		send(ctx, out, scraper.Error(scraper.ParseError(base,
			fmt.Errorf("no %q project_category — every video carries it, so its absence means the taxonomy changed", videosSlug))))
		return
	}

	// Which term to query. The site's own nav uses /collection/{slug}, and the
	// bare /{slug}/ form the sub-brands were published under redirects to it,
	// so a single path segment naming a real term is treated as that
	// collection — which also makes the /videos/ URL land on the full
	// catalogue without a special case.
	catID := videos.ID
	if u, err := url.Parse(studioURL); err == nil && isAliasHost(u.Host) {
		if t, ok := cats.bySlug[aliasCollection]; ok {
			catID = t.ID
			scraper.Debugf(1, "%s: %s is the %q collection", siteID, aliasHost, t.Slug)
		}
	} else if m := collectionURLRe.FindStringSubmatch(studioURL); m != nil {
		t, ok := cats.bySlug[strings.ToLower(m[1])]
		if !ok {
			send(ctx, out, scraper.Error(fmt.Errorf("unknown collection %q", m[1])))
			return
		}
		catID = t.ID
		scraper.Debugf(1, "%s: scraping collection %q", siteID, t.Slug)
	} else if m := firstSegmentRe.FindStringSubmatch(studioURL); m != nil {
		if t, ok := cats.bySlug[strings.ToLower(m[1])]; ok {
			catID = t.ID
			scraper.Debugf(1, "%s: scraping collection %q", siteID, t.Slug)
		}
	}

	// A model page lists that model's projects, so the filter is a slug set
	// applied to the same walk. Doing it this way keeps ids, metadata and the
	// videos-only rule identical to a full scrape.
	var only map[string]bool
	if m := modelURLRe.FindStringSubmatch(studioURL); m != nil {
		only, err = s.fetchModelProjects(ctx, base, m[1])
		if err != nil {
			send(ctx, out, scraper.Error(err))
			return
		}
		scraper.Debugf(1, "%s: model %q lists %d projects", siteID, m[1], len(only))
	}

	items, stopped, failed := s.walkListing(ctx, base, catID, videos.ID, only, opts, out)
	if len(items) == 0 {
		switch {
		case stopped:
			// Everything the walk reached was already stored. Still reported,
			// so the coverage check knows why the run came back empty.
			send(ctx, out, scraper.StoppedEarly())
		case failed || ctx.Err() != nil:
			// walkListing already said what went wrong.
		default:
			send(ctx, out, scraper.Error(scraper.ParseError(base,
				fmt.Errorf("listing returned no videos"))))
		}
		return
	}

	if !send(ctx, out, scraper.Progress(len(items))) {
		return
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), opts.Workers)
	s.fetchDetails(ctx, studioURL, base, cats, items, opts, out)

	if stopped {
		send(ctx, out, scraper.StoppedEarly())
	}
}

// walkListing pages the REST listing newest-first, keeping the videos that pass
// the filters. It returns early once a stored id shows up, which is sound here
// because the endpoint is explicitly date-ordered.
func (s *Scraper) walkListing(ctx context.Context, base string, catID, videosID int, only map[string]bool, opts scraper.ListOpts, out chan<- scraper.SceneResult) (items []listItem, stoppedEarly, failed bool) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return items, false, false
		}
		if page > 1 && !sleep(ctx, opts.Delay) {
			return items, false, false
		}

		u := fmt.Sprintf("%s/wp-json/wp/v2/project?project_category=%d&per_page=%d&page=%d"+
			"&orderby=date&order=desc&_fields=id,date_gmt,link,title,excerpt,project_category",
			base, catID, pageSize, page)
		scraper.Debugf(1, "%s: fetching listing page %d", siteID, page)

		var batch []listItem
		if err := s.getJSON(ctx, u, &batch); err != nil {
			send(ctx, out, scraper.Error(fmt.Errorf("listing page %d: %w", page, err)))
			return items, false, true
		}
		if len(batch) == 0 {
			return items, false, false
		}

		for _, it := range batch {
			// Galleries share the post type and the sub-brand terms; only the
			// videos term separates them.
			if !it.hasCategory(videosID) {
				continue
			}
			if only != nil && !only[it.slug()] {
				continue
			}
			if opts.KnownIDs[it.id()] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, it.id())
				return items, true, false
			}
			items = append(items, it)
		}

		if len(batch) < pageSize {
			return items, false, false
		}
	}
}

func (s *Scraper) fetchDetails(ctx context.Context, studioURL, base string, cats categories, items []listItem, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	work := make(chan listItem)
	var wg sync.WaitGroup
	// LIFO: close(work) ends the workers' range loops, then wg.Wait blocks
	// until they are gone, so bailing on ctx.Done cannot leak them.
	defer wg.Wait()
	defer close(work)

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range work {
				if !sleep(ctx, opts.Delay) {
					return
				}
				scene, err := s.buildScene(ctx, studioURL, base, cats, it)
				if err != nil {
					if !send(ctx, out, scraper.Error(err)) {
						return
					}
					continue
				}
				if !send(ctx, out, scraper.Scene(scene)) {
					return
				}
			}
		}()
	}

	for _, it := range items {
		select {
		case work <- it:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) buildScene(ctx context.Context, studioURL, base string, cats categories, it listItem) (models.Scene, error) {
	sceneURL := s.detailURL(base, it)
	scene := models.Scene{
		ID:        it.id(),
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     cleanText(it.Title.Rendered),
		// The URL actually fetched, not the payload's absolute `link`: the two
		// differ when the operator addresses the site by a host the CMS does
		// not canonicalise to, and recording the unfetched one would also let
		// an offline test quietly reach production.
		URL:         sceneURL,
		Description: cleanText(it.Excerpt.Rendered),
		Studio:      cats.studioFor(it.Categories),
		ScrapedAt:   time.Now().UTC(),
	}
	if t, err := time.Parse("2006-01-02T15:04:05", it.DateGMT); err == nil {
		scene.Date = t.UTC()
	}

	// Performers, tags and the poster are not in the REST payload: the page is
	// built with Divi, whose content lives in post meta the API does not
	// expose. The rendered page carries all three.
	body, err := s.fetchPage(ctx, sceneURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("project %s: %w", it.id(), err)
	}
	d := parseDetail(string(body))
	scene.Performers = d.performers
	scene.Tags = d.tags
	scene.Thumbnail = d.thumbnail

	return scene, nil
}

// detailURL rebuilds the project URL against base so a test server receives the
// detail fetches too; the REST payload's `link` is absolute and production.
func (s *Scraper) detailURL(base string, it listItem) string {
	if slug := it.slug(); slug != "" {
		return base + "/project/" + slug + "/"
	}
	return it.Link
}

// ---- REST types ----

type rendered struct {
	Rendered string `json:"rendered"`
}

type listItem struct {
	PostID     int      `json:"id"`
	DateGMT    string   `json:"date_gmt"`
	Link       string   `json:"link"`
	Title      rendered `json:"title"`
	Excerpt    rendered `json:"excerpt"`
	Categories []int    `json:"project_category"`
}

func (l listItem) id() string { return fmt.Sprintf("%d", l.PostID) }

func (l listItem) slug() string {
	if m := projectSlugRe.FindStringSubmatch(l.Link); m != nil {
		return m[1]
	}
	return ""
}

func (l listItem) hasCategory(id int) bool {
	for _, c := range l.Categories {
		if c == id {
			return true
		}
	}
	return false
}

type term struct {
	ID   int    `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type categories struct {
	bySlug map[string]term
	byID   map[int]term
}

// studioFor picks the sub-brand a project belongs to. Every project also
// carries a content-type term, and those are not brands.
func (c categories) studioFor(ids []int) string {
	for _, id := range ids {
		t, ok := c.byID[id]
		if !ok || typeSlugs[t.Slug] {
			continue
		}
		return html.UnescapeString(t.Name)
	}
	return studioName
}

func (s *Scraper) fetchCategories(ctx context.Context, base string) (categories, error) {
	var terms []term
	u := fmt.Sprintf("%s/wp-json/wp/v2/project_category?per_page=%d&_fields=id,slug,name", base, pageSize)
	if err := s.getJSON(ctx, u, &terms); err != nil {
		return categories{}, err
	}
	c := categories{bySlug: make(map[string]term, len(terms)), byID: make(map[int]term, len(terms))}
	for _, t := range terms {
		c.bySlug[strings.ToLower(t.Slug)] = t
		c.byID[t.ID] = t
	}
	return c, nil
}

// fetchModelProjects reads the project slugs a model page links to. The REST
// API exposes no project→model relation, so this page is the only index of it.
func (s *Scraper) fetchModelProjects(ctx context.Context, base, slug string) (map[string]bool, error) {
	u := base + "/model/" + slug + "/"
	body, err := s.fetchPage(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("model page %s: %w", slug, err)
	}
	out := make(map[string]bool)
	for _, m := range projectSlugRe.FindAllStringSubmatch(string(body), -1) {
		out[m[1]] = true
	}
	if len(out) == 0 {
		return nil, scraper.ParseError(u, fmt.Errorf("model page links to no projects"))
	}
	return out, nil
}

// ---- detail page ----

type detail struct {
	performers []string
	tags       []string
	thumbnail  string
}

// modelAnchorRe matches the cast links in the rendered page, which carry the
// display name. The og:video:actor meta lists the same models but only by URL.
var modelAnchorRe = regexp.MustCompile(`/model/[a-z0-9-]+/?["']?\s*>\s*([^<]+?)\s*</a>`)

func parseDetail(body string) detail {
	var d detail

	// Not parseutil.OpenGraph: this theme writes its OpenGraph tags with
	// `name="og:image"` rather than the standard `property="og:image"`, which
	// that helper (correctly) does not match.
	if m := ogImageRe.FindStringSubmatch(body); m != nil {
		d.thumbnail = html.UnescapeString(m[1])
	}

	seen := make(map[string]bool)
	for _, m := range modelAnchorRe.FindAllStringSubmatch(body, -1) {
		name := cleanText(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		d.performers = append(d.performers, name)
	}

	seenTag := make(map[string]bool)
	for _, m := range videoTagRe.FindAllStringSubmatch(body, -1) {
		t := cleanText(m[1])
		if t == "" || seenTag[t] {
			continue
		}
		seenTag[t] = true
		d.tags = append(d.tags, t)
	}
	return d
}

var (
	ogImageRe  = regexp.MustCompile(`<meta\s+name="og:image"\s+content="([^"]*)"`)
	videoTagRe = regexp.MustCompile(`<meta\s+name="og:video:tag"\s+content="([^"]*)"`)
)

// ---- helpers ----

var tagStripRe = regexp.MustCompile(`<[^>]*>`)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.Join(strings.Fields(s), " ")
}

func (s *Scraper) getJSON(ctx context.Context, u string, v any) error {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		Method:  http.MethodGet,
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.DecodeJSON(resp.Body, v)
}

func (s *Scraper) fetchPage(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		Method:  http.MethodGet,
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}
