// Package nothingbutcurves scrapes nothingbutcurves.com, an Elevated X tour
// whose `item` cards (title, cast, date and runtime) are listing-only: the
// detail pages carry no description and the video is members-only.
// See docs/scrapers.md.
package nothingbutcurves

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "nothingbutcurves"
	studioName = "Nothing But Curves"
	defaultURL = "https://www.nothingbutcurves.com"
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: defaultURL}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"nothingbutcurves.com",
		"nothingbutcurves.com/categories/{slug}.html",
		"nothingbutcurves.com/models/{name}.html",
	}
}

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?nothingbutcurves\.com(?:/|$)`)
	categoryRe = regexp.MustCompile(`/categories/([^/?#]+?)(?:_\d+_[a-z])?\.html`)
	modelRe    = regexp.MustCompile(`/models/([^/?#]+?)(?:_\d+_[a-z])?\.html`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

// listingPath names the listing a studio URL selects. "movies" is the site's
// own name for the whole catalogue and "photos" a gallery index, so neither
// is a filter.
func listingPath(u string) (kind, slug string) {
	if m := modelRe.FindStringSubmatch(u); m != nil && !strings.EqualFold(m[1], "models") {
		return "models", m[1]
	}
	if m := categoryRe.FindStringSubmatch(u); m != nil {
		switch strings.ToLower(m[1]) {
		case "movies", "categories", "photos":
		default:
			return "categories", m[1]
		}
	}
	return "categories", "movies"
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	kind, slug := listingPath(studioURL)
	scraper.Debugf(1, "%s: scraping %s/%s", siteID, kind, slug)

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/%s/%s_%d_d.html", s.base, kind, slug, page)
		resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: pageURL, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
		if err != nil {
			return scraper.PageResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := httpx.ReadBody(resp.Body)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parseListing(body, studioURL, time.Now().UTC())
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

var (
	itemRe     = regexp.MustCompile(`<div class="item">`)
	linkRe     = regexp.MustCompile(`href="((?:https?:)?//[^"]*?/scenes/([^"/]+?)_vids\.html)"`)
	imgRe      = regexp.MustCompile(`<img[^>]+src="([^"]+)"[^>]+alt="([^"]*)"`)
	countRe    = regexp.MustCompile(`(?s)class="count">\s*(\d+:\d+(?::\d+)?)`)
	dateRe     = regexp.MustCompile(`text-muted">\s*(\d{1,2} [A-Za-z]{3} \d{2})\s*<`)
	actorsRe   = regexp.MustCompile(`(?s)class="actors">(.*?)</div>\s*</div>`)
	anchorRe   = regexp.MustCompile(`(?s)<a href="[^"]*/models/[^"]*">(.*?)</a>`)
	tagStrip   = regexp.MustCompile(`<[^>]*>`)
	dateLayout = "2 Jan 06"
)

func parseListing(body []byte, studioURL string, now time.Time) []models.Scene {
	locs := itemRe.FindAllIndex(body, -1)
	var scenes []models.Scene
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		lm := linkRe.FindSubmatch(block)
		im := imgRe.FindSubmatch(block)
		if lm == nil || im == nil {
			continue
		}
		scene := models.Scene{
			ID:        string(lm[2]),
			SiteID:    siteID,
			StudioURL: studioURL,
			Studio:    studioName,
			Title:     clean(string(im[2])),
			URL:       absolute(string(lm[1])),
			Thumbnail: absolute(string(im[1])),
			ScrapedAt: now,
		}
		if scene.Title == "" {
			continue
		}
		if m := countRe.FindSubmatch(block); m != nil {
			scene.Duration = parseutil.ParseDurationColon(string(m[1]))
		}
		if m := dateRe.FindSubmatch(block); m != nil {
			if t, err := parseutil.TryParseDate(string(m[1]), dateLayout); err == nil {
				scene.Date = t.UTC()
			}
		}
		if m := actorsRe.FindSubmatch(block); m != nil {
			for _, a := range anchorRe.FindAllSubmatch(m[1], -1) {
				if name := clean(string(a[1])); name != "" {
					scene.Performers = append(scene.Performers, name)
				}
			}
		}
		scenes = append(scenes, scene)
	}
	return scenes
}

func absolute(u string) string {
	u = html.UnescapeString(u)
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStrip.ReplaceAllString(s, " "))), " ")
}
