package thehabibshow

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
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "thehabibshow"
	studioName = "The Habib Show"
	defaultURL = "https://thehabibshow.com"
	tourPath   = "/tour/"
	pageSize   = 10
)

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(45 * time.Second), base: defaultURL}
}

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"thehabibshow.com",
		"thehabibshow.com/tour/",
		"thehabibshow.com/tour/channels/{id}/{slug}/",
	}
}

var urlRe = regexp.MustCompile(`^https?://(?:www\.)?thehabibshow\.com(?:/.*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return urlRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var channelRe = regexp.MustCompile(`(?i)^/tour/channels/\d+/[^/]+/?$`)

// listingPath is the directory whose pageN.html files hold the feed. Both the
// tour root and a channel paginate the same way, so the only thing a URL mode
// changes is which directory is walked.
func listingPath(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return tourPath
	}
	p := u.Path
	if i := strings.Index(p, "/page"); i >= 0 {
		p = p[:i+1]
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	if channelRe.MatchString(p) {
		return p
	}
	return tourPath
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	path := listingPath(studioURL)
	if path == tourPath {
		scraper.Debugf(1, "%s: scraping the full tour feed", siteID)
	} else {
		scraper.Debugf(1, "%s: scraping channel %s", siteID, path)
	}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s%spage%d.html", s.base, path, page)
		body, err := s.get(ctx, u)
		if err != nil {
			// The feed exposes no page count and the pager on any given page
			// lists only a window of neighbours, so the end is found by asking
			// for one page too many. Erroring there would mark every full run
			// incomplete and block the authoritative Save.
			if page > 1 && isNotFound(err) {
				scraper.Debugf(1, "%s: page %d past the end (404), stopping", siteID, page)
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}

		items := parseFeed(body, u)
		if len(items) == 0 && page == 1 {
			return scraper.PageResult{}, scraper.ParseError(u, errors.New("no articles on the first page"))
		}

		scenes := make([]models.Scene, 0, len(items))
		for _, it := range items {
			scenes = append(scenes, it.scene(studioURL))
		}
		return scraper.PageResult{
			Scenes: scenes,
			Done:   len(items) < pageSize,
		}, nil
	})
}

func isNotFound(err error) bool {
	var se *httpx.StatusError
	return errors.As(err, &se) && se.StatusCode == http.StatusNotFound
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
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

// ---- parsing ----

var (
	articleRe  = regexp.MustCompile(`<article class="article">`)
	titleRe    = regexp.MustCompile(`(?is)<h2 class="small[^"]*">(.*?)</h2>`)
	playerRe   = regexp.MustCompile(`(?is)<div id="player-\d+" class="player"([^>]*)>`)
	dataIDRe   = regexp.MustCompile(`data-id="(\d+)"`)
	posterRe   = regexp.MustCompile(`data-poster="([^"]*)"`)
	linkRe     = regexp.MustCompile(`(?is)<input value="(https?://[^"]*/tour/videos/[^"]*)"`)
	descRe     = regexp.MustCompile(`(?is)<p>(.*?)</p>`)
	runtimeRe  = regexp.MustCompile(`(?i)\bis\s+(\d+)\s*minutes?\b`)
	tagStripRe = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe       = regexp.MustCompile(`\s+`)
)

type feedItem struct {
	id          string
	title       string
	url         string
	thumbnail   string
	description string
	duration    int
}

func (it feedItem) scene(studioURL string) models.Scene {
	return models.Scene{
		ID:          it.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Studio:      studioName,
		Title:       it.title,
		URL:         it.url,
		Thumbnail:   it.thumbnail,
		Description: it.description,
		Duration:    it.duration,
		ScrapedAt:   time.Now().UTC(),
	}
}

// parseFeed reads one pageN.html. Every field lives inside the article, so
// there is no detail fetch: the feed already carries the title, description,
// poster, canonical URL and — inside the copy — the running time.
//
// The site publishes no date anywhere, on the feed or the detail page, so
// Scene.Date is left zero rather than guessed from the id.
func parseFeed(body []byte, pageURL string) []feedItem {
	var items []feedItem
	for _, block := range splitArticles(string(body)) {
		p := playerRe.FindStringSubmatch(block)
		if p == nil {
			continue
		}
		attrs := p[1]
		id := dataIDRe.FindStringSubmatch(attrs)
		if id == nil {
			continue
		}
		it := feedItem{id: id[1]}
		if m := titleRe.FindStringSubmatch(block); m != nil {
			it.title = cleanText(m[1])
		}
		if m := posterRe.FindStringSubmatch(attrs); m != nil {
			it.thumbnail = html.UnescapeString(m[1])
		}
		if m := linkRe.FindStringSubmatch(block); m != nil {
			it.url = html.UnescapeString(m[1])
		}
		if it.url == "" {
			it.url = pageURL
		}
		if m := descRe.FindStringSubmatch(block); m != nil {
			it.description = cleanText(m[1])
			if d := runtimeRe.FindStringSubmatch(it.description); d != nil {
				n, _ := strconv.Atoi(d[1])
				it.duration = n * 60
			}
		}
		if it.title == "" {
			continue
		}
		items = append(items, it)
	}
	return items
}

func splitArticles(page string) []string {
	starts := articleRe.FindAllStringIndex(page, -1)
	if len(starts) == 0 {
		return nil
	}
	blocks := make([]string, len(starts))
	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		blocks[i] = page[loc[0]:end]
	}
	return blocks
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}
