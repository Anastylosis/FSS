package borntobebound

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "borntobebound"
	studioName = "Born to be Bound"
	defaultAPI = "https://borntobebound.com/updates/"
	pageSize   = 100
	// The WordPress `include=` filter takes a comma-separated id list; 100 is
	// the per_page ceiling, so a larger batch would silently truncate.
	termBatch = 100
)

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(60 * time.Second), base: defaultAPI}
}

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"borntobebound.com",
		"borntobebound.com/updates",
		"borntobebound.com/updates/?cat={id}",
		"borntobebound.com/updates/?tag={slug}",
	}
}

var urlRe = regexp.MustCompile(`^https?://(?:www\.)?borntobebound\.com(?:/.*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return urlRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- REST plumbing ----

func (s *Scraper) restURL(route string, q url.Values) string {
	q.Set("rest_route", route)
	return s.base + "?" + q.Encode()
}

type wpRendered struct {
	Rendered string `json:"rendered"`
}

type wpPost struct {
	ID         int        `json:"id"`
	Date       string     `json:"date_gmt"`
	Link       string     `json:"link"`
	Slug       string     `json:"slug"`
	Title      wpRendered `json:"title"`
	Content    wpRendered `json:"content"`
	Categories []int      `json:"categories"`
	Tags       []int      `json:"tags"`
}

type wpTerm struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// termCache resolves WordPress term ids to names. The site has 1938 tags and
// 331 categories; downloading both vocabularies up front costs 24 requests for
// names most runs never use, so ids are resolved on demand and remembered.
type termCache struct {
	s     *Scraper
	route string
	names map[int]string
}

func newTermCache(s *Scraper, route string) *termCache {
	return &termCache{s: s, route: route, names: map[int]string{}}
}

func (c *termCache) resolve(ctx context.Context, ids []int) ([]string, error) {
	var missing []int
	seen := map[int]bool{}
	for _, id := range ids {
		if _, ok := c.names[id]; !ok && !seen[id] {
			seen[id] = true
			missing = append(missing, id)
		}
	}
	for len(missing) > 0 {
		n := min(len(missing), termBatch)
		batch := missing[:n]
		missing = missing[n:]

		strIDs := make([]string, len(batch))
		for i, id := range batch {
			strIDs[i] = strconv.Itoa(id)
		}
		q := url.Values{}
		q.Set("include", strings.Join(strIDs, ","))
		q.Set("per_page", strconv.Itoa(termBatch))
		q.Set("_fields", "id,name")

		var terms []wpTerm
		if err := c.s.getJSON(ctx, c.s.restURL(c.route, q), &terms); err != nil {
			return nil, err
		}
		for _, t := range terms {
			c.names[t.ID] = html.UnescapeString(t.Name)
		}
		// An id the endpoint declined to return would otherwise be re-requested
		// on every page that references it.
		for _, id := range batch {
			if _, ok := c.names[id]; !ok {
				c.names[id] = ""
			}
		}
	}

	var out []string
	dedup := map[string]bool{}
	for _, id := range ids {
		name := c.names[id]
		if name == "" || dedup[name] {
			continue
		}
		dedup[name] = true
		out = append(out, name)
	}
	return out, nil
}

func (s *Scraper) getJSON(ctx context.Context, u string, v any) error {
	return httpx.DoJSON(ctx, s.Client, httpx.Request{
		URL: u,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
			"Accept":     "application/json",
		},
	}, v)
}

// ---- URL modes ----

// filter names the REST query parameter and value that narrows the walk, and
// the label used in debug output.
type filter struct {
	param string
	value string
	label string
}

func (s *Scraper) resolveFilter(ctx context.Context, studioURL string) (filter, error) {
	u, err := url.Parse(studioURL)
	if err != nil {
		return filter{}, nil
	}
	q := u.Query()
	if cat := q.Get("cat"); cat != "" {
		if _, err := strconv.Atoi(cat); err == nil {
			return filter{param: "categories", value: cat, label: "category " + cat}, nil
		}
	}
	if tag := q.Get("tag"); tag != "" {
		id, err := s.termIDBySlug(ctx, "/wp/v2/tags", tag)
		if err != nil {
			return filter{}, err
		}
		return filter{param: "tags", value: strconv.Itoa(id), label: "tag " + tag}, nil
	}
	return filter{}, nil
}

func (s *Scraper) termIDBySlug(ctx context.Context, route, slug string) (int, error) {
	q := url.Values{}
	q.Set("slug", slug)
	q.Set("_fields", "id,name")
	var terms []wpTerm
	if err := s.getJSON(ctx, s.restURL(route, q), &terms); err != nil {
		return 0, err
	}
	if len(terms) == 0 {
		return 0, scraper.AbsentError(s.restURL(route, q), fmt.Errorf("no term with slug %q", slug))
	}
	return terms[0].ID, nil
}

// ---- run ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	f, err := s.resolveFilter(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("%s: resolving filter: %w", siteID, err)):
		case <-ctx.Done():
		}
		return
	}
	if f.param != "" {
		scraper.Debugf(1, "%s: scraping %s", siteID, f.label)
	} else {
		scraper.Debugf(1, "%s: scraping full catalogue", siteID)
	}

	tags := newTermCache(s, "/wp/v2/tags")
	cats := newTermCache(s, "/wp/v2/categories")

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		q := url.Values{}
		q.Set("per_page", strconv.Itoa(pageSize))
		q.Set("page", strconv.Itoa(page))
		q.Set("_fields", "id,date_gmt,link,slug,title,content,categories,tags")
		if f.param != "" {
			q.Set(f.param, f.value)
		}
		u := s.restURL("/wp/v2/posts", q)

		var posts []wpPost
		if err := s.getJSON(ctx, u, &posts); err != nil {
			return scraper.PageResult{}, err
		}

		var scenes []models.Scene
		for _, p := range posts {
			if !hasVideo(p.Content.Rendered) {
				continue
			}
			sc, err := s.toScene(ctx, p, studioURL, tags, cats)
			if err != nil {
				return scraper.PageResult{}, err
			}
			scenes = append(scenes, sc)
		}

		// Journal entries are dropped above, so a page can legitimately yield
		// nothing while the catalogue continues. The walk must not read that as
		// the end — Continue says "this page was empty on purpose".
		return scraper.PageResult{
			Scenes:   scenes,
			Done:     len(posts) < pageSize,
			Continue: len(posts) > 0,
		}, nil
	})
}

// ---- parsing ----

var (
	videoRe     = regexp.MustCompile(`(?i)\.(?:mp4|wmv|mov|m4v)\b`)
	runTimeRe   = regexp.MustCompile(`(?i)run\s*time`)
	durColonRe  = regexp.MustCompile(`(?i)run\s*time\s*:?\s*(\d{1,2}:\d{2}(?::\d{2})?)`)
	durMinRe    = regexp.MustCompile(`(?i)run\s*time\s*:?\s*(?:is\s*)?(?:about\s*)?(\d+)\s*(?:minutes?|mins?\b)`)
	durHourRe   = regexp.MustCompile(`(?i)run\s*time\s*:?\s*(?:is\s*)?(?:about\s*)?(\d+)\s*hours?(?:\s*(?:and\s*)?(\d+)\s*(?:minutes?|mins?))?`)
	thumbRe     = regexp.MustCompile(`(?i)<a[^>]+href=['"]([^'"]*/wp-content/uploads/[^'"]+\.(?:jpe?g|png|webp))['"]`)
	thumbImgRe  = regexp.MustCompile(`(?i)<img[^>]+src=['"]([^'"]*/wp-content/uploads/[^'"]+\.(?:jpe?g|png|webp))['"]`)
	paraRe      = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
	tagStripRe  = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe        = regexp.MustCompile(`\s+`)
	starringRe  = regexp.MustCompile(`(?i)\bstarring\s+(.+)$`)
	splitCastRe = regexp.MustCompile(`(?i)\s*(?:,|\band\b|&amp;|&)\s*`)
)

func hasVideo(content string) bool {
	return videoRe.MatchString(content) || runTimeRe.MatchString(content)
}

func (s *Scraper) toScene(ctx context.Context, p wpPost, studioURL string, tags, cats *termCache) (models.Scene, error) {
	title := cleanText(p.Title.Rendered)
	body := p.Content.Rendered

	sc := models.Scene{
		ID:          strconv.Itoa(p.ID),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Studio:      studioName,
		Title:       title,
		URL:         p.Link,
		Description: description(body),
		Duration:    duration(body),
		Thumbnail:   thumbnail(body),
		ScrapedAt:   time.Now().UTC(),
	}
	if title == "" {
		sc.Title = p.Slug
	}
	if t, err := time.Parse("2006-01-02T15:04:05", p.Date); err == nil {
		sc.Date = t.UTC()
	}

	names, err := cats.resolve(ctx, p.Categories)
	if err != nil {
		return models.Scene{}, err
	}
	// Categories mix performer names with bondage descriptors ("Ballgag",
	// "Chair tie") and nothing in the feed separates them, so they are stored
	// as categories rather than guessed into performers.
	sc.Categories = names

	sc.Tags, err = tags.resolve(ctx, p.Tags)
	if err != nil {
		return models.Scene{}, err
	}
	sc.Performers = starring(title)
	return sc, nil
}

// starring reads the cast off the handful of titles that name it. Most posts
// do not, and the term vocabularies cannot supply it, so an absent cast is
// left absent.
func starring(title string) []string {
	m := starringRe.FindStringSubmatch(title)
	if m == nil {
		return nil
	}
	var out []string
	for _, part := range splitCastRe.Split(m[1], -1) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

func duration(body string) int {
	if m := durColonRe.FindStringSubmatch(body); m != nil {
		if d := parseutil.ParseDurationColon(m[1]); d > 0 {
			return d
		}
	}
	if m := durHourRe.FindStringSubmatch(body); m != nil {
		h, _ := strconv.Atoi(m[1])
		mins, _ := strconv.Atoi(m[2])
		return h*3600 + mins*60
	}
	if m := durMinRe.FindStringSubmatch(body); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n * 60
	}
	return 0
}

// thumbnail prefers the gallery's full-size link, but some galleries only
// carry the 150x150 <img>. The resized file is stored verbatim rather than
// guessed back to an original that may not exist under that name.
func thumbnail(body string) string {
	if m := thumbRe.FindStringSubmatch(body); m != nil {
		return html.UnescapeString(m[1])
	}
	if m := thumbImgRe.FindStringSubmatch(body); m != nil {
		return html.UnescapeString(m[1])
	}
	return ""
}

// description keeps the prose paragraphs and drops the two boilerplate lines
// every update carries: the members-only download link and the run time.
func description(body string) string {
	var paras []string
	for _, m := range paraRe.FindAllStringSubmatch(body, -1) {
		txt := cleanText(m[1])
		if txt == "" || runTimeRe.MatchString(txt) {
			continue
		}
		low := strings.ToLower(txt)
		if strings.Contains(low, "click here to download") || strings.Contains(low, "right click") {
			continue
		}
		paras = append(paras, txt)
	}
	return strings.Join(paras, "\n\n")
}
