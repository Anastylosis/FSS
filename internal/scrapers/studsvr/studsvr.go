// Package studsvr scrapes studsvr.com, RICHARD.XXX's gay VR brand. It runs on
// the Naughty America suite CMS like richard.xxx but with a different tour
// template, so it does not share richardxxx's card parser. See docs/scrapers.md.
package studsvr

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
	siteID        = "studsvr"
	studioName    = "StudsVR"
	defaultPath   = "/scenes"
	detailWorkers = 4
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?studsvr\.com(?:/|$|\?)`)
	starRe     = regexp.MustCompile(`^/pornstar/([^/?#]+)`)
	categoryRe = regexp.MustCompile(`^/category/([^/?#]+)`)

	rowSplitRe  = regexp.MustCompile(`<div class="row feed-row">`)
	sceneHref   = regexp.MustCompile(`href="([^"]*/scene/[^"?#]*-(\d+))(?:\?[^"]*)?"`)
	titleRe     = regexp.MustCompile(`(?s)class="h5 feed-title">\s*<a href="[^"]*/scene/[^"]*">(.*?)</a>`)
	anchorRe    = regexp.MustCompile(`(?s)<a [^>]*>.*?</a>`)
	thumbRe     = regexp.MustCompile(`(?s)class="col col-6 feed-thumb">.*?<img src="([^"]+)"`)
	durationRe  = regexp.MustCompile(`class="video-duration">\s*(\d+)\s*min`)
	excerptRe   = regexp.MustCompile(`(?s)class="feed-excerpt">(.*?)</p>`)
	synopsisRe  = regexp.MustCompile(`(?s)class="video-synopsis">(.*?)</p>`)
	categoryTag = regexp.MustCompile(`(?s)class="video-category"[^>]*>(.*?)</a>`)
	profileRe   = regexp.MustCompile(`(?s)<a href="[^"]*/pornstar/[^"]*">.*?<span>(.*?)</span>`)
	h1Re        = regexp.MustCompile(`(?s)class="h5 video-title">(.*?)</h1>`)
	starLinkRe  = regexp.MustCompile(`(?s)<a href="[^"]*/pornstar/[^"]*">(.*?)</a>`)
	ldJSONRe    = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

// Scraper implements scraper.StudioScraper for studsvr.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the StudsVR scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://www.studsvr.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"studsvr.com",
		"studsvr.com/scenes",
		"studsvr.com/pornstar/{performer}",
		"studsvr.com/category/{category}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// listingPath maps an operator URL onto the listing path: a performer, a
// category, or the whole catalogue.
func listingPath(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return defaultPath
	}
	if m := starRe.FindStringSubmatch(u.Path); m != nil {
		return "/pornstar/" + m[1]
	}
	if m := categoryRe.FindStringSubmatch(u.Path); m != nil {
		return "/category/" + m[1]
	}
	return defaultPath
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	path := listingPath(studioURL)
	scraper.Debugf(1, "%s: listing %s", siteID, path)
	now := time.Now().UTC()
	seen := map[string]bool{}

	// The listing is not date-ordered, so a known ID says nothing about
	// what follows it.
	walkOpts := opts
	walkOpts.KnownIDs = nil

	scraper.Paginate(ctx, walkOpts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?page=%d", s.base, path, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parseListing(body)
		if page == 1 && len(scenes) == 0 && path == defaultPath {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scenes on the first page"))
		}

		var fresh []models.Scene
		for _, sc := range scenes {
			if !seen[sc.ID] {
				seen[sc.ID] = true
				sc.SiteID = siteID
				sc.StudioURL = studioURL
				sc.Studio = studioName
				sc.URL = s.rebase(sc.URL)
				sc.ScrapedAt = now
				fresh = append(fresh, sc)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		s.enrich(ctx, fresh, opts, out)
		return scraper.PageResult{Scenes: fresh}, nil
	})
}

// parseListing reads the feed rows of a listing page. Each row carries the
// title, runtime, synopsis, first names and a few categories; the scene page
// has the full set plus the release date.
func parseListing(body string) []models.Scene {
	marks := rowSplitRe.FindAllStringIndex(body, -1)
	scenes := make([]models.Scene, 0, len(marks))
	for i, m := range marks {
		end := len(body)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		block := body[m[0]:end]

		href := sceneHref.FindStringSubmatch(block)
		if href == nil {
			continue
		}
		sc := models.Scene{ID: href[2], URL: href[1]}
		// Performer pages head each row with the cast instead of the title;
		// the scene page fills it in then.
		if t := titleRe.FindStringSubmatch(block); t != nil {
			sc.Title = cleanText(t[1])
		}
		if th := thumbRe.FindStringSubmatch(block); th != nil {
			sc.Thumbnail = absURL(html.UnescapeString(th[1]))
		}
		sc.Duration = parseDuration(block)
		if e := excerptRe.FindStringSubmatch(block); e != nil {
			sc.Description = cleanText(e[1])
		}
		sc.Tags = categories(block)
		for _, p := range profileRe.FindAllStringSubmatch(block, -1) {
			if name := cleanText(p[1]); name != "" {
				sc.Performers = append(sc.Performers, name)
			}
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

type detail struct {
	title       string
	date        time.Time
	description string
	performers  []string
	tags        []string
	duration    int
}

type videoObject struct {
	Description   string `json:"description"`
	DatePublished string `json:"datePublished"`
}

// parseDetail reads a scene page: full performer names from the heading, the
// whole category list, and the release date from the JSON-LD VideoObject.
func parseDetail(body string) (detail, bool) {
	var d detail
	ok := false
	for _, m := range ldJSONRe.FindAllStringSubmatch(body, -1) {
		var v videoObject
		if err := json.Unmarshal([]byte(m[1]), &v); err != nil || v.DatePublished == "" {
			continue
		}
		if t, err := parseutil.TryParseDate(v.DatePublished, "2006-01-02 15:04:05", "2006-01-02"); err == nil {
			d.date = t
		}
		d.description = cleanText(v.Description)
		ok = true
		break
	}
	if h := h1Re.FindStringSubmatch(body); h != nil {
		for _, p := range starLinkRe.FindAllStringSubmatch(h[1], -1) {
			if name := cleanText(p[1]); name != "" {
				d.performers = append(d.performers, name)
			}
		}
		// The heading reads "<cast links> in <Title>".
		d.title = strings.TrimPrefix(cleanText(anchorRe.ReplaceAllString(h[1], " ")), "in ")
		ok = true
	}
	if syn := synopsisRe.FindStringSubmatch(body); syn != nil {
		d.description = cleanText(syn[1])
	}
	d.tags = categories(body)
	d.duration = parseDuration(body)
	return d, ok
}

// enrich completes each scene from its page; a field the page lacks keeps
// the listing's value.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
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
			d, ok := parseDetail(body)
			if !ok {
				select {
				case out <- scraper.Error(scraper.ParseError(scenes[i].URL, fmt.Errorf("no scene details found"))):
				case <-ctx.Done():
				}
				return
			}
			sc := &scenes[i]
			sc.Date = d.date
			if sc.Title == "" {
				sc.Title = d.title
			}
			if d.description != "" {
				sc.Description = d.description
			}
			if len(d.performers) > 0 {
				sc.Performers = d.performers
			}
			if len(d.tags) > 0 {
				sc.Tags = d.tags
			}
			if d.duration > 0 {
				sc.Duration = d.duration
			}
		}(i)
	}
	wg.Wait()
}

func categories(block string) []string {
	var tags []string
	for _, t := range categoryTag.FindAllStringSubmatch(block, -1) {
		if tag := cleanText(t[1]); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func parseDuration(block string) int {
	if d := durationRe.FindStringSubmatch(block); d != nil {
		if n, err := strconv.Atoi(d[1]); err == nil {
			return n * 60
		}
	}
	return 0
}

// rebase keeps the scene URL on the host being scraped, so the scraper stays
// pointable at a test server.
func (s *Scraper) rebase(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	return s.base + p.Path
}

func absURL(u string) string {
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
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
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}
