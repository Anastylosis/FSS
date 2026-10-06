// Package kvssitemaputil scrapes KVS-style tour sites whose video sitemap
// carries the full catalogue. See docs/scrapers.md.
package kvssitemaputil

import (
	"context"
	"encoding/xml"
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
	detailWorkers = 4
	// batchSize bounds how many scenes wait on their detail pages before
	// being sent, so output starts streaming early.
	batchSize   = 24
	sitemapPath = "/sitemap/?type=videos&from_links_videos=1"
)

// SiteConfig describes one site. PerformerRe and TagRe have one capture group
// and run over the detail page, or over Region's first group when set.
type SiteConfig struct {
	SiteID     string
	Base       string
	StudioName string
	// Filters are the path sections that list a subset of videos
	// (/models/{slug}/, /tags/{slug}/, ...).
	Filters        []string
	Region         *regexp.Regexp
	PerformerRe    *regexp.Regexp
	TagRe          *regexp.Regexp
	SkipPerformers []string
}

type Scraper struct {
	cfg     SiteConfig
	Client  *http.Client
	matchRe *regexp.Regexp
	pathRe  *regexp.Regexp
}

var (
	idRe        = regexp.MustCompile(`/videos?/(\d+)/`)
	listHrefRe  = regexp.MustCompile(`href="[^"]*/videos?/(\d+)/`)
	listStartRe = regexp.MustCompile(`id="list_videos_common_videos`)
	listEndRe   = regexp.MustCompile(`id="list_albums_|_webcams_strip"`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
	localeRe    = regexp.MustCompile(`^[a-z]{2}$`)
)

func New(cfg SiteConfig) *Scraper {
	u, _ := url.Parse(cfg.Base)
	host := strings.TrimPrefix(u.Host, "www.")
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(45 * time.Second),
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(host) + `(?:[/?#]|$)`),
		pathRe:  regexp.MustCompile(`^/(` + strings.Join(cfg.Filters, "|") + `)/([^/?#]+)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	u, _ := url.Parse(s.cfg.Base)
	host := strings.TrimPrefix(u.Host, "www.")
	p := []string{host}
	for _, f := range s.cfg.Filters {
		p = append(p, host+"/"+f+"/{slug}/")
	}
	return p
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type sitemap struct {
	URLs []struct {
		Loc   string `xml:"loc"`
		Video struct {
			Thumb    string   `xml:"thumbnail_loc"`
			Title    string   `xml:"title"`
			Desc     string   `xml:"description"`
			Duration int      `xml:"duration"`
			Date     string   `xml:"publication_date"`
			Cats     []string `xml:"category"`
		} `xml:"video"`
	} `xml:"url"`
}

// filter returns the section and slug a filtered URL selects, if any. A
// leading two-letter locale segment is ignored.
func (s *Scraper) filter(studioURL string) (section, slug string, ok bool) {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "", "", false
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) > 1 && localeRe.MatchString(segs[0]) {
		segs = segs[1:]
	}
	m := s.pathRe.FindStringSubmatch("/" + strings.Join(segs, "/"))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	smURL := s.cfg.Base + sitemapPath
	body, err := s.fetch(ctx, smURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	scenes, err := s.parseSitemap(body, studioURL, time.Now().UTC())
	if err != nil {
		send(ctx, out, scraper.Error(scraper.ParseError(smURL, err)))
		return
	}

	if section, slug, ok := s.filter(studioURL); ok {
		scraper.Debugf(1, "%s: filtering by %s/%s", s.cfg.SiteID, section, slug)
		ids, err := s.filterIDs(ctx, section, slug, opts)
		if err != nil {
			send(ctx, out, scraper.Error(err))
			return
		}
		kept := scenes[:0]
		for _, sc := range scenes {
			if ids[sc.ID] {
				kept = append(kept, sc)
			}
		}
		scenes = kept
	}

	if !send(ctx, out, scraper.Progress(len(scenes))) {
		return
	}

	var fresh []models.Scene
	for _, sc := range scenes {
		if !opts.KnownIDs[sc.ID] {
			fresh = append(fresh, sc)
		}
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(fresh), workers)
	for start := 0; start < len(fresh); start += batchSize {
		batch := fresh[start:min(start+batchSize, len(fresh))]
		s.enrich(ctx, batch, opts, workers, out)
		for _, sc := range batch {
			if !send(ctx, out, scraper.Scene(sc)) {
				return
			}
		}
	}
}

// parseSitemap builds the base scenes in sitemap order.
func (s *Scraper) parseSitemap(data []byte, studioURL string, now time.Time) ([]models.Scene, error) {
	var sm sitemap
	if err := xml.Unmarshal(data, &sm); err != nil {
		return nil, err
	}
	if len(sm.URLs) == 0 {
		return nil, fmt.Errorf("sitemap has no entries")
	}
	scenes := make([]models.Scene, 0, len(sm.URLs))
	for _, e := range sm.URLs {
		m := idRe.FindStringSubmatch(e.Loc)
		if m == nil {
			continue
		}
		sc := models.Scene{
			ID:          m[1],
			SiteID:      s.cfg.SiteID,
			StudioURL:   studioURL,
			Studio:      s.cfg.StudioName,
			Title:       strings.TrimSpace(html.UnescapeString(e.Video.Title)),
			URL:         strings.TrimSpace(e.Loc),
			Description: strings.TrimSpace(html.UnescapeString(e.Video.Desc)),
			Thumbnail:   strings.TrimSpace(e.Video.Thumb),
			Duration:    e.Video.Duration,
			ScrapedAt:   now,
		}
		if d, err := parseutil.TryParseDate(strings.TrimSpace(e.Video.Date), "2006-01-02"); err == nil {
			sc.Date = d
		}
		for _, c := range e.Video.Cats {
			if c = strings.TrimSpace(html.UnescapeString(c)); c != "" {
				sc.Tags = append(sc.Tags, c)
			}
		}
		scenes = append(scenes, sc)
	}
	return scenes, nil
}

// filterIDs walks a model/tag/category listing and returns the video IDs on
// it. Pages past the end repeat or empty out, so the walk stops on a page with
// nothing new.
func (s *Scraper) filterIDs(ctx context.Context, section, slug string, opts scraper.ListOpts) (map[string]bool, error) {
	ids := map[string]bool{}
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return ids, err
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return ids, ctx.Err()
		}
		u := fmt.Sprintf("%s/%s/%s/", s.cfg.Base, section, slug)
		if page > 1 {
			u += strconv.Itoa(page) + "/"
		}
		body, err := s.fetch(ctx, u)
		if err != nil {
			if page > 1 {
				return ids, nil
			}
			return ids, err
		}
		added := 0
		for _, id := range listingIDs(string(body)) {
			if !ids[id] {
				ids[id] = true
				added++
			}
		}
		if added == 0 {
			return ids, nil
		}
	}
}

// listingIDs returns the video IDs inside a page's main video list, leaving
// out sidebars and related blocks.
func listingIDs(page string) []string {
	loc := listStartRe.FindStringIndex(page)
	if loc == nil {
		return nil
	}
	page = page[loc[0]:]
	if end := listEndRe.FindStringIndex(page); end != nil {
		page = page[:end[0]]
	}
	var ids []string
	seen := map[string]bool{}
	for _, m := range listHrefRe.FindAllStringSubmatch(page, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	return ids
}

func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, workers int, out chan<- scraper.SceneResult) {
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
				send(ctx, out, scraper.Error(err))
				return
			}
			s.applyDetail(&scenes[i], string(body))
		}(i)
	}
	wg.Wait()
}

// applyDetail adds the performers and tags only the detail page carries.
func (s *Scraper) applyDetail(sc *models.Scene, page string) {
	region := page
	if s.cfg.Region != nil {
		m := s.cfg.Region.FindStringSubmatch(page)
		if m == nil {
			return
		}
		region = m[1]
	}
	for _, m := range s.cfg.PerformerRe.FindAllStringSubmatch(region, -1) {
		name := cleanText(m[1])
		if name != "" && !contains(s.cfg.SkipPerformers, name) && !contains(sc.Performers, name) {
			sc.Performers = append(sc.Performers, name)
		}
	}
	if s.cfg.TagRe != nil {
		for _, m := range s.cfg.TagRe.FindAllStringSubmatch(region, -1) {
			if tag := cleanText(m[1]); tag != "" && !contains(sc.Tags, tag) {
				sc.Tags = append(sc.Tags, tag)
			}
		}
	}
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}
