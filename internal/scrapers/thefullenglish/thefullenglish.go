// Package thefullenglish scrapes The Full English (thefullenglish.net), a
// custom gay-male tour. The /updates listing mixes videos and photosets; only
// cards carrying the video icon are scenes. Cast is not on the update pages,
// so it is derived from the model pages, which list their updates. See
// docs/scrapers.md.
package thefullenglish

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
	siteID     = "thefullenglish"
	studioName = "The Full English"
)

var siteBase = "https://www.thefullenglish.net"

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?thefullenglish\.net(?:/|$)`)

	articleRe   = regexp.MustCompile(`(?s)<article id="post-(\d+)".*?</article>`)
	cardLinkRe  = regexp.MustCompile(`<a href="([^"]*/updates/[^"]+)" title="([^"]*)"`)
	cardDateRe  = regexp.MustCompile(`</strong>\s*([A-Z][a-z]+ \d{1,2}, \d{4})`)
	cardImageRe = regexp.MustCompile(`class="entry-featured-image" src="([^"]+)"`)
	cardTextRe  = regexp.MustCompile(`(?s)class="entry-content">(.*?)<hr`)
	detailRe    = regexp.MustCompile(`(?s)class="update_view_content">.*?<h1[^>]*>(.*?)</h1>(.*?)<div class="call-to-action-button"`)
	modelLinkRe = regexp.MustCompile(`href="[^"]*/models/([^"/]+)">\s*<img[^>]*>\s*<span>([^<]*)</span>`)
	nextRe      = regexp.MustCompile(`rel="next"`)
	tagRe       = regexp.MustCompile(`<[^>]+>`)
)

const videoIcon = `icon-facetime-video"`

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"thefullenglish.net", "thefullenglish.net/updates"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type card struct {
	id, url, title, date, thumb, excerpt string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	var cast map[string][]string

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetch(ctx, fmt.Sprintf("%s/updates?page=%d", siteBase, page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards, hasNext := parseListing(body)
		var scenes []models.Scene
		for _, c := range cards {
			sc := toScene(c, studioURL, now)
			if !opts.KnownIDs[c.id] {
				if cast == nil {
					scraper.Debugf(1, "%s: loading model pages for cast", siteID)
					cast = s.fetchCast(ctx, opts)
				}
				if scraper.Pace(ctx, opts.Delay) {
					s.enrich(ctx, &sc, onSiteBase(c.url))
				}
				sc.Performers = cast[c.id]
			}
			scenes = append(scenes, sc)
		}
		// A page of photosets alone is still a page, so only the listing's own
		// "next" link ends the walk.
		return scraper.PageResult{Scenes: scenes, Done: !hasNext, Continue: hasNext}, nil
	})
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func parseListing(body []byte) (cards []card, hasNext bool) {
	for _, m := range articleRe.FindAllSubmatch(body, -1) {
		block := m[0]
		if !strings.Contains(string(block), videoIcon) {
			continue
		}
		l := cardLinkRe.FindSubmatch(block)
		if l == nil {
			continue
		}
		c := card{id: string(m[1]), url: html.UnescapeString(string(l[1])), title: clean(string(l[2]))}
		if d := cardDateRe.FindSubmatch(block); d != nil {
			c.date = string(d[1])
		}
		if i := cardImageRe.FindSubmatch(block); i != nil {
			c.thumb = html.UnescapeString(string(i[1]))
		}
		if t := cardTextRe.FindSubmatch(block); t != nil {
			c.excerpt = clean(string(t[1]))
		}
		cards = append(cards, c)
	}
	return cards, nextRe.Match(body)
}

func (s *Scraper) enrich(ctx context.Context, sc *models.Scene, u string) {
	body, err := s.fetch(ctx, u)
	if err != nil {
		scraper.Debugf(1, "%s: detail %s: %v", siteID, u, err)
		return
	}
	if d := parseDetail(body); d != "" {
		sc.Description = d
	}
}

func parseDetail(body []byte) string {
	m := detailRe.FindSubmatch(body)
	if m == nil {
		return ""
	}
	text := strings.ReplaceAll(string(m[2]), "</p>", "\n")
	return clean(text)
}

// fetchCast maps update post ID to the models whose pages list it. A failure
// only costs the cast, so it is logged rather than failing the scrape.
func (s *Scraper) fetchCast(ctx context.Context, opts scraper.ListOpts) map[string][]string {
	cast := map[string][]string{}
	body, err := s.fetch(ctx, siteBase+"/models")
	if err != nil {
		scraper.Debugf(1, "%s: model list: %v", siteID, err)
		return cast
	}
	for _, m := range modelLinkRe.FindAllSubmatch(body, -1) {
		name := clean(string(m[2]))
		for page := 1; ; page++ {
			if !scraper.Pace(ctx, opts.Delay) {
				return cast
			}
			u := fmt.Sprintf("%s/models/%s", siteBase, m[1])
			if page > 1 {
				u += fmt.Sprintf("?page=%d", page)
			}
			pb, err := s.fetch(ctx, u)
			if err != nil {
				scraper.Debugf(1, "%s: model %s: %v", siteID, m[1], err)
				break
			}
			for _, a := range articleRe.FindAllSubmatch(pb, -1) {
				id := string(a[1])
				cast[id] = append(cast[id], name)
			}
			if !nextRe.Match(pb) {
				break
			}
		}
	}
	return cast
}

func toScene(c card, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          c.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       c.title,
		URL:         c.url,
		Description: c.excerpt,
		Thumbnail:   c.thumb,
		Studio:      studioName,
		ScrapedAt:   now,
	}
	if d, err := parseutil.TryParseDate(c.date, "January 2, 2006"); err == nil {
		sc.Date = d.UTC()
	}
	return sc
}

func clean(s string) string {
	s = html.UnescapeString(tagRe.ReplaceAllString(s, " "))
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// onSiteBase re-roots an absolute listing link on siteBase, so detail fetches
// follow the base the walk uses.
func onSiteBase(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Path == "" {
		return u
	}
	return siteBase + p.EscapedPath()
}
