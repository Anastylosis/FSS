// Package whornyfilms scrapes whornyfilms.com, a custom server-rendered tour.
// See docs/scrapers.md.
package whornyfilms

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "whornyfilms"
	studioName = "Whorny Films"
	// detailWorkers: the date and tags are only on each scene's own page.
	detailWorkers = 4
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?whornyfilms\.com(?:/|$|\?)`)
	modelRe = regexp.MustCompile(`^/models/([^/?#]+)`)
	tagRe   = regexp.MustCompile(`^/tag/([^/?#]+)`)

	cardSplitRe = regexp.MustCompile(`<div class="group video-card`)
	cardLinkRe  = regexp.MustCompile(`href="/videos/([^"/?#]+)"[^>]*class="[^"]*card-thumb-link`)
	cardImgRe   = regexp.MustCompile(`<img src="([^"]+)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<h3 class="card-info-title">\s*<a[^>]*>(.*?)</a>`)
	cardModelRe = regexp.MustCompile(`(?s)<p class="card-info-models">(.*?)</p>`)
	cardMetaRe  = regexp.MustCompile(`(?s)<span class="card-info-meta">(.*?)</span>`)
	anchorRe    = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)

	uploadDateRe = regexp.MustCompile(`"uploadDate":"([^"]+)"`)
	detailTagRe  = regexp.MustCompile(`(?s)<a href="/tag/[^"]+"[^>]*>(.*?)</a>`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
)

var dateLayouts = []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", time.RFC3339}

// Scraper implements scraper.StudioScraper for whornyfilms.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Whorny Films scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: "https://whornyfilms.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"whornyfilms.com",
		"whornyfilms.com/videos",
		"whornyfilms.com/models/{slug}",
		"whornyfilms.com/tag/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type card struct {
	slug       string
	title      string
	thumb      string
	performers []string
	duration   int
	views      int
}

// listPath maps an operator URL onto the listing path to walk.
func listPath(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "/videos"
	}
	if m := modelRe.FindStringSubmatch(u.Path); m != nil {
		return "/models/" + m[1]
	}
	if m := tagRe.FindStringSubmatch(u.Path); m != nil {
		return "/tag/" + m[1]
	}
	return "/videos"
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	path := listPath(studioURL)
	scraper.Debugf(1, "%s: listing %s", siteID, path)
	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?sort=latest&page=%d", s.base, path, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 && !strings.Contains(body, `id="videos-grid"`) {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no video grid on the first page"))
		}

		scenes := make([]models.Scene, 0, len(cards))
		for _, c := range cards {
			if seen[c.slug] {
				continue
			}
			seen[c.slug] = true
			scenes = append(scenes, s.baseScene(c, studioURL, now))
		}
		if len(scenes) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		fresh := make([]*models.Scene, 0, len(scenes))
		for i := range scenes {
			if !opts.KnownIDs[scenes[i].ID] {
				fresh = append(fresh, &scenes[i])
			}
		}
		s.enrich(ctx, fresh, opts, out)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func parseListing(body string) []card {
	idx := cardSplitRe.FindAllStringIndex(body, -1)
	cards := make([]card, 0, len(idx))
	for i, m := range idx {
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		block := body[m[0]:end]

		link := cardLinkRe.FindStringSubmatch(block)
		if link == nil {
			continue
		}
		c := card{slug: link[1]}
		if t := cardTitleRe.FindStringSubmatch(block); t != nil {
			c.title = cleanText(t[1])
		}
		if c.title == "" {
			continue
		}
		if im := cardImgRe.FindStringSubmatch(block); im != nil {
			c.thumb = html.UnescapeString(im[1])
		}
		if pm := cardModelRe.FindStringSubmatch(block); pm != nil {
			for _, a := range anchorRe.FindAllStringSubmatch(pm[1], -1) {
				if name := cleanText(a[1]); name != "" {
					c.performers = append(c.performers, name)
				}
			}
		}
		// Views come first, then the runtime.
		metas := cardMetaRe.FindAllStringSubmatch(block, -1)
		if len(metas) >= 1 {
			c.views = parseCount(cleanText(metas[0][1]))
		}
		if len(metas) >= 2 {
			c.duration = parseutil.ParseDurationColon(cleanText(metas[1][1]))
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:         c.slug,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     studioName,
		Title:      c.title,
		URL:        s.base + "/videos/" + c.slug,
		Thumbnail:  resolveURL(s.base, c.thumb),
		Performers: c.performers,
		Duration:   c.duration,
		Views:      c.views,
		ScrapedAt:  now,
	}
}

func (s *Scraper) enrich(ctx context.Context, scenes []*models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(scenes) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for _, sc := range scenes {
		wg.Add(1)
		go func(sc *models.Scene) {
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
			body, err := s.fetch(ctx, sc.URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(sc, body)
		}(sc)
	}
	wg.Wait()
}

// applyDetail adds the upload date and tags from a scene page.
func applyDetail(sc *models.Scene, body string) {
	if m := uploadDateRe.FindStringSubmatch(body); m != nil {
		if d, err := parseutil.TryParseDate(m[1], dateLayouts...); err == nil {
			sc.Date = d
		}
	}
	// Other /tag/ links (navigation) sit outside the Tags section.
	_, tagSection, found := strings.Cut(body, `text-muted">Tags</h2>`)
	if !found {
		return
	}
	for _, m := range detailTagRe.FindAllStringSubmatch(tagSection, -1) {
		if tag := cleanText(m[1]); tag != "" && !contains(sc.Tags, tag) {
			sc.Tags = append(sc.Tags, tag)
		}
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func parseCount(s string) int {
	n, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(s), ",", ""))
	if err != nil {
		return 0
	}
	return n
}

func resolveURL(base, ref string) string {
	switch {
	case ref == "":
		return ""
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return ref
	case strings.HasPrefix(ref, "//"):
		return "https:" + ref
	case strings.HasPrefix(ref, "/"):
		return base + ref
	default:
		return base + "/" + ref
	}
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
