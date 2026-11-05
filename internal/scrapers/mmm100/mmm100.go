// Package mmm100 scrapes MMM100 (mmm100.com) from its Google video sitemap.
// See docs/scrapers.md.
package mmm100

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID       = "mmm100"
	studioName   = "MMM100"
	maxSitemapMB = 64
)

var siteBase = "https://mmm100.com"

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?mmm100\.com(?:/|$)`)
	slugRe  = regexp.MustCompile(`/video/([^/?#]+)`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(120 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string { return []string{"mmm100.com"} }

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type urlset struct {
	URLs []struct {
		Loc   string `xml:"loc"`
		Video struct {
			Thumbnail   string `xml:"http://www.google.com/schemas/sitemap-video/1.1 thumbnail_loc"`
			Title       string `xml:"http://www.google.com/schemas/sitemap-video/1.1 title"`
			Description string `xml:"http://www.google.com/schemas/sitemap-video/1.1 description"`
			Duration    int    `xml:"http://www.google.com/schemas/sitemap-video/1.1 duration"`
			Published   string `xml:"http://www.google.com/schemas/sitemap-video/1.1 publication_date"`
		} `xml:"http://www.google.com/schemas/sitemap-video/1.1 video"`
	} `xml:"url"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, _ int) (scraper.PageResult, error) {
		u := siteBase + "/sitemap.videos.xml"
		resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
		if err != nil {
			return scraper.PageResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := httpx.ReadBodyN(resp.Body, maxSitemapMB<<20)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes, err := parseSitemap(body, studioURL, now)
		if err != nil {
			return scraper.PageResult{}, scraper.ParseError(u, err)
		}
		scraper.Debugf(1, "%s: %d videos in sitemap", siteID, len(scenes))
		return scraper.PageResult{Scenes: scenes, Total: len(scenes), Done: true}, nil
	})
}

// parseSitemap returns the scenes newest first, so a KnownIDs early stop
// still has the ordering Paginate assumes.
func parseSitemap(body []byte, studioURL string, now time.Time) ([]models.Scene, error) {
	var set urlset
	if err := parseutil.DecodeXML(body, &set); err != nil {
		return nil, err
	}
	if len(set.URLs) == 0 {
		return nil, fmt.Errorf("no <url> entries in sitemap")
	}
	scenes := make([]models.Scene, 0, len(set.URLs))
	seen := map[string]bool{}
	for _, e := range set.URLs {
		m := slugRe.FindStringSubmatch(e.Loc)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		sc := models.Scene{
			ID:          m[1],
			SiteID:      siteID,
			StudioURL:   studioURL,
			Title:       clean(e.Video.Title),
			URL:         strings.TrimSpace(e.Loc),
			Description: clean(e.Video.Description),
			Thumbnail:   strings.TrimSpace(e.Video.Thumbnail),
			Duration:    e.Video.Duration,
			Studio:      studioName,
			ScrapedAt:   now,
		}
		if sc.Title == "" {
			sc.Title = m[1]
		}
		if len(e.Video.Published) >= 10 {
			if t, err := time.Parse("2006-01-02", e.Video.Published[:10]); err == nil {
				sc.Date = t
			}
		}
		scenes = append(scenes, sc)
	}
	sort.SliceStable(scenes, func(i, j int) bool { return scenes[i].Date.After(scenes[j].Date) })
	return scenes, nil
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}
