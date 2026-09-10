package innofsin

import (
	"context"
	"errors"
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
	"github.com/Anastylosis/FSS/scraper"
)

// postsPerPage is the WP REST page size. WP caps per_page at 100.
const postsPerPage = 100

type siteConfig struct {
	id      string
	domain  string
	studio  string
	matchRe *regexp.Regexp
}

var sites = []siteConfig{
	{
		id:      "mydeepdarksecret",
		domain:  "mydeepdarksecret.com",
		studio:  "My Deep Dark Secret",
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?mydeepdarksecret\.com(?:/|$)`),
	},
	{
		id:      "richardmannsworld",
		domain:  "richardmannsworld.com",
		studio:  "Richard Mann's World",
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?richardmannsworld\.com(?:/|$)`),
	},
	{
		id:      "bbctitans",
		domain:  "bbctitans.com",
		studio:  "BBC Titans",
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?bbctitans\.com(?:/|$)`),
	},
	{
		id:      "richardmannevents",
		domain:  "richardmannevents.com",
		studio:  "Richard Mann Events",
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?richardmannevents\.com(?:/|$)`),
	},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newScraper(cfg))
	}
}

type siteScraper struct {
	cfg    siteConfig
	client *http.Client
}

func newScraper(cfg siteConfig) *siteScraper {
	return &siteScraper{
		cfg: cfg,
		// The WP REST listing is fetched with ?_embed at per_page=100, which
		// is ~900 KB a page and slow to assemble. A 30s client timeout raced
		// the origin and cancelled pages that were about to arrive, failing
		// the walk mid-catalogue; leave the deadline to the server.
		client: httpx.NewClient(90 * time.Second),
	}
}

var _ scraper.StudioScraper = (*siteScraper)(nil)

func (s *siteScraper) ID() string               { return s.cfg.id }
func (s *siteScraper) Patterns() []string       { return []string{s.cfg.domain} }
func (s *siteScraper) MatchesURL(u string) bool { return s.cfg.matchRe.MatchString(u) }

func (s *siteScraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// --- WP REST API types ---

type wpPost struct {
	ID            int     `json:"id"`
	Date          string  `json:"date"`
	Slug          string  `json:"slug"`
	Link          string  `json:"link"`
	Title         wpField `json:"title"`
	Content       wpField `json:"content"`
	FeaturedMedia int     `json:"featured_media"`
	Categories    []int   `json:"categories"`
	Embedded      wpEmbed `json:"_embedded"`
}

type wpField struct {
	Rendered string `json:"rendered"`
}

type wpEmbed struct {
	Media []wpMedia  `json:"wp:featuredmedia"`
	Terms [][]wpTerm `json:"wp:term"`
}

type wpMedia struct {
	SourceURL string `json:"source_url"`
}

type wpTerm struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// --- runner ---

func (s *siteScraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := "https://" + s.cfg.domain
	if u, err := url.Parse(studioURL); err == nil && u.Host != "" {
		base = u.Scheme + "://" + u.Host
	}

	posts, err := s.fetchAllPosts(ctx, base, opts, out)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	if len(posts) == 0 {
		return
	}
	scraper.Debugf(1, "%s: %d posts from API, fetching detail pages", s.cfg.id, len(posts))

	select {
	case out <- scraper.Progress(len(posts)):
	case <-ctx.Done():
		return
	}

	s.fetchDetailsAndEmit(ctx, base, posts, studioURL, opts, out)
}

func (s *siteScraper) fetchAllPosts(ctx context.Context, base string, opts scraper.ListOpts, _ chan<- scraper.SceneResult) ([]wpPost, error) {
	var all []wpPost
	page := 1
	for {
		if ctx.Err() != nil {
			return all, ctx.Err()
		}

		apiURL := fmt.Sprintf("%s/wp-json/wp/v2/posts?per_page=%d&page=%d&_embed", base, postsPerPage, page)
		posts, total, totalPages, err := s.fetchPage(ctx, apiURL)
		if err != nil {
			// When the post count is an exact multiple of the page size, the
			// loop requests one page past the end and WP answers HTTP 400.
			// Returning that as an error made the caller discard every post
			// already fetched, leaving the site unscrapeable until its count
			// drifted off the boundary.
			//
			// Only that specific 400 means end-of-list. Treating *any* page>1
			// failure as the end is what made a 429 or 502 on page 3 return the
			// first two pages with err == nil — and under `--full` the store
			// then hard-deletes every scene the run never reached, price history
			// included, while reporting success. Mirrors fotoroutil.fetchTerms.
			var se *httpx.StatusError
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusBadRequest {
				scraper.Debugf(1, "%s: API page %d past end (400), stopping", s.cfg.id, page)
				break
			}
			return all, fmt.Errorf("API page %d: %w", page, err)
		}

		if page == 1 && total > 0 {
			scraper.Debugf(1, "%s: %d total posts", s.cfg.id, total)
		}

		all = append(all, posts...)

		if len(posts) < postsPerPage || (totalPages > 0 && page >= totalPages) {
			break
		}
		if opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return all, ctx.Err()
			}
		}
		page++
	}
	return all, nil
}

// fetchPage returns one page of posts plus the total post and page counts from
// the WP REST pagination headers.
func (s *siteScraper) fetchPage(ctx context.Context, apiURL string) ([]wpPost, int, int, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     apiURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	total, _ := strconv.Atoi(resp.Header.Get("X-WP-Total"))
	totalPages, _ := strconv.Atoi(resp.Header.Get("X-WP-TotalPages"))

	var posts []wpPost
	if err := httpx.DecodeJSON(resp.Body, &posts); err != nil {
		return nil, 0, 0, fmt.Errorf("decoding posts: %w", err)
	}
	return posts, total, totalPages, nil
}

// --- detail page for performers ---

var performerLinkRe = regexp.MustCompile(`<a[^>]+href="[^"]*/pornstars/[^"]*"[^>]*>([^<]+)</a>`)

func (s *siteScraper) fetchPerformers(ctx context.Context, pageURL string) ([]string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var performers []string
	for _, m := range performerLinkRe.FindAllSubmatch(body, -1) {
		name := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if name != "" && !seen[name] {
			seen[name] = true
			performers = append(performers, name)
		}
	}
	return performers, nil
}

// --- detail fetching worker pool ---

func (s *siteScraper) fetchDetailsAndEmit(ctx context.Context, _ string, posts []wpPost, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.id, len(posts), workers)

	work := make(chan wpPost, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for post := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				performers, ferr := s.fetchPerformers(ctx, post.Link)
				if ferr != nil {
					select {
					case out <- scraper.Error(fmt.Errorf("detail %d: %w", post.ID, ferr)):
					case <-ctx.Done():
						return
					}
					continue
				}
				scene := buildScene(post, performers, s.cfg, studioURL, now)
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, post := range posts {
		id := strconv.Itoa(post.ID)
		if opts.KnownIDs[id] {
			scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.id, id)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			break
		}
		select {
		case work <- post:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
}

func buildScene(post wpPost, performers []string, cfg siteConfig, studioURL string, now time.Time) models.Scene {
	title := html.UnescapeString(post.Title.Rendered)

	var date time.Time
	if post.Date != "" {
		date, _ = time.Parse("2006-01-02T15:04:05", post.Date)
		date = date.UTC()
	}

	desc := stripHTML(post.Content.Rendered)

	var thumbnail string
	if len(post.Embedded.Media) > 0 && post.Embedded.Media[0].SourceURL != "" {
		thumbnail = post.Embedded.Media[0].SourceURL
	}

	var categories []string
	if len(post.Embedded.Terms) > 0 {
		for _, t := range post.Embedded.Terms[0] {
			name := t.Name
			lower := strings.ToLower(name)
			if lower == "scenes" || lower == "videos" || lower == "uncategorized" {
				continue
			}
			categories = append(categories, name)
		}
	}

	return models.Scene{
		ID:          strconv.Itoa(post.ID),
		SiteID:      cfg.id,
		StudioURL:   studioURL,
		Title:       title,
		URL:         post.Link,
		Thumbnail:   thumbnail,
		Date:        date,
		Description: desc,
		Performers:  performers,
		Tags:        categories,
		Studio:      cfg.studio,
		ScrapedAt:   now,
	}
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

func stripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(s)
}
