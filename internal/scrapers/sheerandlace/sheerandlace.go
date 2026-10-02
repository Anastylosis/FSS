// Package sheerandlace scrapes sheerandlace.com, an Elevated X tour whose
// listing cards carry every field. See docs/scrapers.md.
package sheerandlace

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "sheerandlace"
	studioName = "Sheer and Lace"
	defaultURL = "https://sheerandlace.com"
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?sheerandlace\.com(?:/|$)`)
	sortRe  = regexp.MustCompile(`/tour/categories/movies/\d+/([a-z]+)/?`)
	modelRe = regexp.MustCompile(`/tour/models/([^/?#]+)\.html`)

	cardSplitRe = regexp.MustCompile(`<div class="item-video`)
	linkRe      = regexp.MustCompile(`(?s)<h4>\s*<a href="([^"]+/trailers/[^"]+)"[^>]*>(.*?)</a>`)
	thumbRe     = regexp.MustCompile(`<img src="([^"]+)"[^>]*class="video_placeholder"`)
	thumbIDRe   = regexp.MustCompile(`/(\d+)-1x\.\w+$`)
	castRe      = regexp.MustCompile(`(?s)<div class="pornstars">(.*?)</div>`)
	castItemRe  = regexp.MustCompile(`(?s)<a [^>]*>(.*?)</a>`)
	timeRe      = regexp.MustCompile(`(?s)<div class="time">(.*?)</div>`)
	clockRe     = regexp.MustCompile(`\d+(?::\d{2}){1,2}\s*$`)
	dateRe      = regexp.MustCompile(`<div class="date">\s*(\d{4}-\d{2}-\d{2})`)
	setsIDRe    = regexp.MustCompile(`sets\.php\?id=(\d+)`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

// Scraper implements scraper.StudioScraper for sheerandlace.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Sheer and Lace scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: defaultURL}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"sheerandlace.com",
		"sheerandlace.com/tour/categories/movies/{page}/{sort}/",
		"sheerandlace.com/tour/models/{slug}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type card struct {
	id, path, title, thumb, date string
	duration                     int
	performers                   []string
}

// listing is what an operator URL selects: the whole catalogue in one sort
// order, or one model's videos.
type listing struct {
	sort  string
	model string
}

func resolveListing(studioURL string) listing {
	u, err := url.Parse(studioURL)
	if err != nil {
		return listing{sort: "latest"}
	}
	if m := modelRe.FindStringSubmatch(u.Path); m != nil {
		return listing{model: m[1]}
	}
	if m := sortRe.FindStringSubmatch(u.Path); m != nil {
		return listing{sort: m[1]}
	}
	return listing{sort: "latest"}
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	l := resolveListing(studioURL)
	scraper.Debugf(1, "%s: listing %+v", siteID, l)

	now := time.Now().UTC()
	seen := map[string]bool{}
	modelSetID := ""

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/tour/categories/movies/%d/%s/", s.base, page, l.sort)
		if l.model != "" {
			pageURL = fmt.Sprintf("%s/tour/models/%s.html", s.base, l.model)
			if page > 1 {
				pageURL = fmt.Sprintf("%s/tour/sets.php?id=%s&page=%d&s=&g=", s.base, modelSetID, page)
			}
		}
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if l.model != "" && page == 1 {
			m := setsIDRe.FindStringSubmatch(body)
			if m == nil {
				return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no model id on the model page"))
			}
			modelSetID = m[1]
		}

		cards := parseListing(body)
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}
		var scenes []models.Scene
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			scenes = append(scenes, s.toScene(c, studioURL, now))
		}
		return scraper.PageResult{Scenes: scenes, Done: len(scenes) == 0}, nil
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

		link := linkRe.FindStringSubmatch(block)
		if link == nil {
			continue
		}
		c := card{path: link[1], title: cleanText(link[2])}
		if u, err := url.Parse(html.UnescapeString(link[1])); err == nil {
			c.path = u.Path
		}
		if th := thumbRe.FindStringSubmatch(block); th != nil {
			c.thumb = html.UnescapeString(th[1])
			if id := thumbIDRe.FindStringSubmatch(c.thumb); id != nil {
				c.id = id[1]
			}
		}
		if c.id == "" {
			c.id = strings.TrimSuffix(c.path[strings.LastIndex(c.path, "/")+1:], ".html")
		}
		if cast := castRe.FindStringSubmatch(block); cast != nil {
			for _, a := range castItemRe.FindAllStringSubmatch(cast[1], -1) {
				if name := cleanText(a[1]); name != "" {
					c.performers = append(c.performers, name)
				}
			}
		}
		// The time div doubles as a photo count: "125&nbsp;Photos, 15:51".
		if t := timeRe.FindStringSubmatch(block); t != nil {
			if clock := clockRe.FindString(cleanText(t[1])); clock != "" {
				c.duration = parseutil.ParseDurationColon(clock)
			}
		}
		if d := dateRe.FindStringSubmatch(block); d != nil {
			c.date = d[1]
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) toScene(c card, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:         c.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     studioName,
		Title:      c.title,
		URL:        s.base + c.path,
		Performers: c.performers,
		Duration:   c.duration,
		ScrapedAt:  now,
	}
	if c.thumb != "" {
		sc.Thumbnail = s.base + c.thumb
	}
	if d, err := parseutil.TryParseDate(c.date, "2006-01-02"); err == nil {
		sc.Date = d
	}
	return sc
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
