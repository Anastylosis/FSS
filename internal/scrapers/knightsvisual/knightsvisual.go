// Package knightsvisual scrapes knights-visual.com (FS. Knights Visual and its
// labels) through the WordPress REST API. See docs/scrapers.md.
package knightsvisual

import (
	"context"
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

const (
	siteID = "knightsvisual"
	// liveBase is www because the apex host serves a certificate for a
	// different name.
	liveBase = "https://www.knights-visual.com"
	// listPerPage is the REST maximum.
	listPerPage = 100
	// detailBatch is how many posts one include= request fetches.
	detailBatch   = 25
	detailWorkers = 4
)

// labels maps the /works/{label}/ path segment to the label's display name.
var labels = map[string]string{
	"furasupi":    "Furasupi",
	"glum":        "GLUM",
	"hakudakudou": "HAKUDAKUDOU",
	"paraphilia":  "Paraphilia",
	"runrun":      "Runrun",
	"trigger-j":   "Trigger J",
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?knights-visual\.com(?:[/?#]|$)`)

	tableRowRe = regexp.MustCompile(`(?s)<td class="label"[^>]*>(.*?)</td>\s*<td class="data">(.*?)</td>`)
	// The blurb is the first paragraph after the info table.
	blurbRe = regexp.MustCompile(`(?s)</table>\s*(?:</div>\s*)?<p>(.*?)</p>`)
	imgRe   = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	brRe    = regexp.MustCompile(`(?i)<br\s*/?>`)
	tagRe   = regexp.MustCompile(`<[^>]+>`)

	durationRe = regexp.MustCompile(`(\d+)\s*分(?:\s*(\d+)\s*秒)?`)
	castSplit  = regexp.MustCompile(`[\s\x{3000}、,，/／]+`)
)

// Scraper implements scraper.StudioScraper for knights-visual.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Knights Visual scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(60 * time.Second), base: liveBase}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"knights-visual.com",
		"knights-visual.com/works/{label}/",
		"knights-visual.com/works/{label}/{series}/",
		"knights-visual.com/category/works/{label}/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// listEntry is one post of the light listing.
type listEntry struct {
	ID   int    `json:"id"`
	Link string `json:"link"`
	Date string `json:"date"`
}

type post struct {
	ID    int    `json:"id"`
	Link  string `json:"link"`
	Date  string `json:"date"`
	Title struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Content struct {
		Rendered string `json:"rendered"`
	} `json:"content"`
}

// worksPrefix returns the path under /works/ that an operator URL selects, such
// as "furasupi/" or "furasupi/nonstop/"; empty means every work.
func worksPrefix(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return ""
	}
	p := strings.Trim(u.Path, "/")
	p = strings.TrimPrefix(p, "category/")
	rest, ok := strings.CutPrefix(p, "works/")
	if !ok || rest == "" {
		return ""
	}
	return strings.ToLower(rest) + "/"
}

// workPath returns the post's path below /works/, or "" for anything else
// (blog, news and download-store posts).
func workPath(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	rest, ok := strings.CutPrefix(u.Path, "/works/")
	if !ok {
		return ""
	}
	return rest
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	prefix := worksPrefix(studioURL)
	scraper.Debugf(1, "%s: works prefix %q", siteID, prefix)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		entries, totalPages, err := s.fetchList(ctx, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		var want []listEntry
		for _, e := range entries {
			wp := workPath(e.Link)
			if wp == "" || !strings.HasPrefix(strings.ToLower(wp), prefix) {
				continue
			}
			want = append(want, e)
		}
		done := page >= totalPages || len(entries) < listPerPage
		if len(want) == 0 {
			return scraper.PageResult{Continue: !done, Done: done}, nil
		}

		posts, err := s.fetchDetails(ctx, want, opts)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, 0, len(posts))
		for _, p := range posts {
			sc, ok := s.buildScene(p, studioURL, now)
			if !ok {
				scraper.Debugf(1, "%s: skipping %s (no catalogue number)", siteID, p.Link)
				continue
			}
			scenes = append(scenes, sc)
		}
		return scraper.PageResult{Scenes: scenes, Continue: !done, Done: done}, nil
	})
}

func (s *Scraper) apiGet(ctx context.Context, rawURL string, v any) (http.Header, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
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

func (s *Scraper) fetchList(ctx context.Context, page int) ([]listEntry, int, error) {
	u := fmt.Sprintf("%s/wp-json/wp/v2/posts?per_page=%d&page=%d&_fields=id,link,date", s.base, listPerPage, page)
	var entries []listEntry
	h, err := s.apiGet(ctx, u, &entries)
	if err != nil {
		return nil, 0, err
	}
	total, _ := strconv.Atoi(h.Get("X-WP-TotalPages"))
	if page == 1 && len(entries) == 0 {
		return nil, 0, scraper.ParseError(u, fmt.Errorf("empty post listing"))
	}
	return entries, total, nil
}

// fetchDetails loads the full posts for a listing page in id batches, keeping
// the listing's newest-first order.
func (s *Scraper) fetchDetails(ctx context.Context, want []listEntry, opts scraper.ListOpts) ([]post, error) {
	var batches [][]listEntry
	for i := 0; i < len(want); i += detailBatch {
		batches = append(batches, want[i:min(i+detailBatch, len(want))])
	}
	results := make([][]post, len(batches))
	errs := make([]error, len(batches))

	sem := make(chan struct{}, scraper.WorkerCount(opts, detailWorkers))
	var wg sync.WaitGroup
	for i, b := range batches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				errs[i] = ctx.Err()
				return
			}
			ids := make([]string, len(b))
			for j, e := range b {
				ids[j] = strconv.Itoa(e.ID)
			}
			u := fmt.Sprintf("%s/wp-json/wp/v2/posts?per_page=%d&orderby=include&include=%s&_fields=id,link,date,title,content",
				s.base, len(b), strings.Join(ids, ","))
			_, errs[i] = s.apiGet(ctx, u, &results[i])
		}()
	}
	wg.Wait()

	var posts []post
	for i := range batches {
		if errs[i] != nil {
			return nil, errs[i]
		}
		posts = append(posts, results[i]...)
	}
	return posts, nil
}

// buildScene turns a post into a scene; posts without a 商品番号 table row are
// label notices and purchase-guide pages, not releases.
func (s *Scraper) buildScene(p post, studioURL string, now time.Time) (models.Scene, bool) {
	fields := parseTable(p.Content.Rendered)
	code := fields["商品番号"]
	if code == "" {
		return models.Scene{}, false
	}

	sc := models.Scene{
		ID:          code,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       cleanText(p.Title.Rendered),
		URL:         s.base + pathOf(p.Link),
		Studio:      studioName(p.Link, fields["レーベル"]),
		Description: parseBlurb(p.Content.Rendered),
		Duration:    parseDuration(fields["収録時間"]),
		ScrapedAt:   now,
	}
	cast := fields["出演者"]
	if cast == "" {
		cast = fields["出演"]
	}
	sc.Performers = splitCast(cast)
	if t, err := time.Parse("2006-01-02T15:04:05", p.Date); err == nil {
		sc.Date = t.UTC()
	}
	if m := imgRe.FindStringSubmatch(p.Content.Rendered); m != nil {
		sc.Thumbnail = absURL(s.base, html.UnescapeString(m[1]))
	}
	return sc, true
}

// parseTable reads the 商品番号 / レーベル / 出演者 / 収録時間 info table. Cast
// cells keep their <br> separators as newlines.
func parseTable(content string) map[string]string {
	fields := map[string]string{}
	for _, m := range tableRowRe.FindAllStringSubmatch(content, -1) {
		key := cleanText(m[1])
		if key == "" {
			continue
		}
		fields[key] = cleanText(brRe.ReplaceAllString(m[2], "\n"))
	}
	return fields
}

func parseBlurb(content string) string {
	m := blurbRe.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	return strings.Join(strings.Fields(cleanText(m[1])), " ")
}

// parseDuration reads "130分", "5分30秒" and "120分（2時間）", using the first
// minutes figure.
func parseDuration(v string) int {
	m := durationRe.FindStringSubmatch(v)
	if m == nil {
		return 0
	}
	mins, _ := strconv.Atoi(m[1])
	secs := 0
	if m[2] != "" {
		secs, _ = strconv.Atoi(m[2])
	}
	return mins*60 + secs
}

func splitCast(v string) []string {
	var out []string
	for _, n := range castSplit.Split(v, -1) {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// studioName names the label from the post's /works/{label}/ segment, falling
// back to the page's own レーベル value for labels outside the known six.
func studioName(link, pageLabel string) string {
	slug, _, _ := strings.Cut(workPath(link), "/")
	if name, ok := labels[slug]; ok {
		return name
	}
	return pageLabel
}

func pathOf(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	return u.EscapedPath()
}

func absURL(base, ref string) string {
	switch {
	case ref == "":
		return ""
	case strings.HasPrefix(ref, "//"):
		return "https:" + ref
	case strings.HasPrefix(ref, "/"):
		return base + ref
	default:
		return ref
	}
}

func cleanText(s string) string {
	s = html.UnescapeString(tagRe.ReplaceAllString(s, ""))
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
