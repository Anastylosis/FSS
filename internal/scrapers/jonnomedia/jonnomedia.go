// Package jonnomedia scrapes the Jonno Media sites (UK Naked Men, Butch Dixon,
// Young Bastards, Spritzz), which share one CMS. See docs/scrapers.md.
package jonnomedia

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

const detailWorkers = 4

type siteConfig struct {
	SiteID     string
	Domain     string
	Host       string
	StudioName string
	// Prefix is the tour's path under the host: "/tour" where the root belongs
	// to the members' area, empty otherwise.
	Prefix string
	// ModelPath is the first segment of a model page; only the root-level
	// sites have linkable ones.
	ModelPath string
	Aliases   []string
}

var sites = []siteConfig{
	{SiteID: "uknakedmen", Domain: "uknakedmen.com", Host: "www.uknakedmen.com", StudioName: "UK Naked Men", Prefix: "/tour", Aliases: []string{"ukhotjocks.com"}},
	{SiteID: "butchdixon", Domain: "butchdixon.com", Host: "www.butchdixon.com", StudioName: "Butch Dixon", Prefix: "/tour"},
	{SiteID: "youngbastards", Domain: "youngbastards.com", Host: "youngbastards.com", StudioName: "Young Bastards", ModelPath: "twink"},
	{SiteID: "spritzz", Domain: "spritzz.com", Host: "spritzz.com", StudioName: "Spritzz", ModelPath: "twink"},
}

var (
	cardSplitRe = regexp.MustCompile(`<div class="itemv[ "]`)
	hrefRe      = regexp.MustCompile(`href="([^"]+)"`)
	videoHrefRe = regexp.MustCompile(`^/?video/[^/"]+/(\d+)$`)
	refstatRe   = regexp.MustCompile(`^(?:\./)?refstat\.php\?lid=(\d+)&sid=\d+$`)
	imgRe       = regexp.MustCompile(`<img src="([^"]+)"`)
	nameRe      = regexp.MustCompile(`(?s)<div class="nm-name[^"]*">(.*?)</div>`)

	h2Re       = regexp.MustCompile(`(?s)<div class="videonewinfo">\s*<h2>(.*?)</h2>`)
	infoBodyRe = regexp.MustCompile(`(?s)<div class="videonewinfo">\s*(?:<h5>.*?</h5>)?\s*<p>(.*?)</p>`)
	h5Re       = regexp.MustCompile(`(?s)<h5>(.*?)</h5>`)
	durationRe = regexp.MustCompile(`Duration:\s*(\d+):(\d+)(?::(\d+))?`)
	releasedRe = regexp.MustCompile(`Released:\s*([A-Za-z]+ \d{1,2}, \d{4})`)
	castRe     = regexp.MustCompile(`(?s)<p>\s*Cast:\s*(.*?)</p>`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)

	dateLayouts = []string{"January 2, 2006", "Jan 2, 2006"}
)

// Scraper implements scraper.StudioScraper for one Jonno Media site.
type Scraper struct {
	cfg     siteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp
	modelRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func newScraper(cfg siteConfig) *Scraper {
	domains := append([]string{cfg.Domain}, cfg.Aliases...)
	for i, d := range domains {
		domains[i] = strings.ReplaceAll(d, ".", `\.`)
	}
	client := httpx.NewClient(45 * time.Second)
	client.CheckRedirect = escapeRedirect
	s := &Scraper{
		cfg:     cfg,
		client:  client,
		base:    "https://" + cfg.Host,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?(?:` + strings.Join(domains, "|") + `)(?:/|$)`),
	}
	if cfg.ModelPath != "" {
		s.modelRe = regexp.MustCompile(`^/` + cfg.ModelPath + `/[^/]+/\d+/?$`)
	}
	return s
}

// escapeRedirect re-encodes the spaces that refstat.php leaves raw in its
// Location header, which the server answers with a 400 when echoed back.
func escapeRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return http.ErrUseLastResponse
	}
	req.URL.RawQuery = strings.ReplaceAll(req.URL.RawQuery, " ", "%20")
	return nil
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newScraper(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	p := []string{s.cfg.Domain, s.cfg.Domain + s.cfg.Prefix + "/?videos/{page}"}
	if s.cfg.Prefix != "" {
		p = append(p, s.cfg.Domain+s.cfg.Prefix+"/show.php?a={site}_{page}")
	}
	if s.cfg.ModelPath != "" {
		p = append(p, s.cfg.Domain+"/"+s.cfg.ModelPath+"/{slug}/{id}")
	}
	return append(p, s.cfg.Aliases...)
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type card struct {
	id    string
	path  string
	title string
	thumb string
}

// modelPath returns the model page path the operator URL names, if any.
func (s *Scraper) modelPath(studioURL string) string {
	if s.modelRe == nil {
		return ""
	}
	u, err := url.Parse(studioURL)
	if err != nil || !s.modelRe.MatchString(u.Path) {
		return ""
	}
	return strings.TrimSuffix(u.Path, "/")
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	model := s.modelPath(studioURL)
	if model != "" {
		scraper.Debugf(1, "%s: scraping model page %s", s.cfg.SiteID, model)
	}
	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s/?videos/%d", s.base, s.cfg.Prefix, page)
		if model != "" {
			pageURL = s.base + model
		}
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 && model == "" {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}

		fresh := cards[:0]
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			fresh = append(fresh, c)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		enrichTo := len(fresh)
		for i, c := range fresh {
			if opts.KnownIDs[c.id] {
				enrichTo = i
				break
			}
		}
		scenes := make([]models.Scene, len(fresh))
		for i, c := range fresh {
			scenes[i] = s.baseScene(c, studioURL, now)
		}
		s.enrich(ctx, scenes[:enrichTo], opts, out)
		return scraper.PageResult{Scenes: scenes, Done: model != ""}, nil
	})
}

// parseListing reads the scene cards of a listing or model page.
func parseListing(body string) []card {
	parts := cardSplitRe.Split(body, -1)
	var cards []card
	for _, blk := range parts[1:] {
		if end := strings.Index(blk, "<!-- end_link -->"); end >= 0 {
			blk = blk[:end]
		}
		var c card
		for _, h := range hrefRe.FindAllStringSubmatch(blk, -1) {
			href := html.UnescapeString(h[1])
			if m := videoHrefRe.FindStringSubmatch(href); m != nil {
				c.id, c.path = m[1], "/"+strings.TrimPrefix(href, "/")
			} else if m := refstatRe.FindStringSubmatch(href); m != nil {
				c.id, c.path = m[1], "/"+strings.TrimPrefix(href, "./")
			}
			if c.id != "" {
				break
			}
		}
		if m := nameRe.FindStringSubmatch(blk); m != nil {
			c.title = cleanText(m[1])
		}
		if c.id == "" || c.title == "" {
			continue
		}
		if m := imgRe.FindStringSubmatch(blk); m != nil {
			c.thumb = html.UnescapeString(m[1])
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	// Refstat links are relative to the tour directory, video links to the root.
	pageURL := s.base + c.path
	if strings.HasPrefix(c.path, "/refstat.php") {
		pageURL = s.base + s.cfg.Prefix + c.path
	}
	return models.Scene{
		ID:        c.id,
		SiteID:    s.cfg.SiteID,
		StudioURL: studioURL,
		Studio:    s.cfg.StudioName,
		Title:     c.title,
		URL:       pageURL,
		Thumbnail: s.resolve(c.thumb),
		ScrapedAt: now,
	}
}

func (s *Scraper) resolve(ref string) string {
	switch {
	case ref == "":
		return ""
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return ref
	case strings.HasPrefix(ref, "//"):
		return "https:" + ref
	case strings.HasPrefix(ref, "/"):
		return s.base + ref
	}
	return s.base + s.cfg.Prefix + "/" + ref
}

func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(scenes) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(scenes), workers)
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
			applyDetail(&scenes[i], body)
		}(i)
	}
	wg.Wait()
}

// applyDetail folds a detail page into a scene. The root-level sites carry
// duration, release date and cast; the /tour sites only title and synopsis.
func applyDetail(sc *models.Scene, body string) {
	if m := h2Re.FindStringSubmatch(body); m != nil {
		if t := cleanText(m[1]); t != "" {
			sc.Title = t
		}
	}
	if m := infoBodyRe.FindStringSubmatch(body); m != nil {
		sc.Description = cleanText(m[1])
	}
	if m := h5Re.FindStringSubmatch(body); m != nil {
		if d := durationRe.FindStringSubmatch(m[1]); d != nil {
			sc.Duration = clockSeconds(d[1], d[2], d[3])
		}
		if r := releasedRe.FindStringSubmatch(m[1]); r != nil {
			if t, err := parseutil.TryParseDate(r[1], dateLayouts...); err == nil {
				sc.Date = t
			}
		}
	}
	if m := castRe.FindStringSubmatch(body); m != nil {
		for _, name := range strings.Split(cleanText(m[1]), ",") {
			if name = strings.TrimSpace(name); name != "" {
				sc.Performers = append(sc.Performers, name)
			}
		}
	}
}

// clockSeconds reads "mm:ss" or "hh:mm:ss".
func clockSeconds(a, b, c string) int {
	x, _ := strconv.Atoi(a)
	y, _ := strconv.Atoi(b)
	if c == "" {
		return x*60 + y
	}
	z, _ := strconv.Atoi(c)
	return x*3600 + y*60 + z
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
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
