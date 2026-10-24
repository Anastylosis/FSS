// Package buddhabang scrapes buddhabangxxx.com, a WordPress site on the VidMov
// theme. The HTML archive drives pagination and supplies thumbnails; the
// WordPress RSS feed of the same archive supplies release dates and full
// descriptions. See docs/scrapers.md.
package buddhabang

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID      = "buddhabang"
	studioName  = "Buddha Bang XXX"
	defaultBase = "https://buddhabangxxx.com"
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?buddhabangxxx\.com(?:/|$)`)
	categoryRe = regexp.MustCompile(`^/video-category/([^/]+)`)

	articleRe   = regexp.MustCompile(`<article id="post-(\d+)" class="([^"]*)"`)
	titleLinkRe = regexp.MustCompile(`class="post-listing-title" href="([^"]+)" title="([^"]*)"`)
	imgRe       = regexp.MustCompile(`<img[^>]*class="blog-img"[^>]*>`)
	srcRe       = regexp.MustCompile(`\ssrc="([^"]+)"`)
	srcsetRe    = regexp.MustCompile(`srcset="([^"]+)"`)
	catClassRe  = regexp.MustCompile(`vidmov_video_category-([a-z0-9-]+)`)
	totalRe     = regexp.MustCompile(`data-append-id="#beeteam368_main-archive-page"\s+data-total-pages="(\d+)"`)
	tagRe       = regexp.MustCompile(`<[^>]+>`)
	spaceRe     = regexp.MustCompile(`\s+`)
)

// Scraper implements scraper.StudioScraper for buddhabangxxx.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Buddha Bang scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: defaultBase}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"buddhabangxxx.com",
		"buddhabangxxx.com/video/",
		"buddhabangxxx.com/video-category/{slug}/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// listingPath maps an operator URL onto the archive it selects: a video
// category, or the whole video archive. The whole archive skips the
// "shorts" category, which holds short clips rather than scenes.
func listingPath(studioURL string) (path string, skipShorts bool) {
	u, err := url.Parse(studioURL)
	if err == nil {
		if m := categoryRe.FindStringSubmatch(u.Path); m != nil {
			return "/video-category/" + m[1] + "/", false
		}
	}
	return "/video/", true
}

type card struct {
	id         string
	url        string
	title      string
	thumb      string
	categories []string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	path, skipShorts := listingPath(studioURL)
	scraper.Debugf(1, "%s: listing %s", siteID, path)

	feed := &feedIndex{s: s, path: path, delay: opts.Delay, items: map[string]feedItem{}}
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + path
		if page > 1 {
			pageURL += "page/" + strconv.Itoa(page) + "/"
		}
		body, err := s.get(ctx, pageURL)
		if err != nil {
			if page > 1 && isNotFound(err) {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		cards, totalPages := parseListing(body)
		if len(cards) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(pageURL, errors.New("no video cards on listing page"))
			}
			return scraper.PageResult{Done: true}, nil
		}

		var scenes []models.Scene
		for _, c := range cards {
			if skipShorts && onlyShorts(c.categories) {
				continue
			}
			item, err := feed.lookup(ctx, linkKey(c.url))
			if err != nil {
				return scraper.PageResult{}, err
			}
			scenes = append(scenes, toScene(studioURL, c, item, now))
		}
		res := scraper.PageResult{
			Scenes:   scenes,
			Done:     page >= totalPages,
			Continue: true,
		}
		if page == 1 {
			res.Total = totalPages * len(cards)
		}
		return res, nil
	})
}

func onlyShorts(cats []string) bool {
	for _, c := range cats {
		if c != "shorts" {
			return false
		}
	}
	return len(cats) > 0
}

func toScene(studioURL string, c card, item feedItem, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          c.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       c.title,
		URL:         c.url,
		Thumbnail:   c.thumb,
		Studio:      studioName,
		Date:        item.date,
		Description: item.description,
		ScrapedAt:   now,
	}
	if sc.Title == "" {
		sc.Title = item.title
	}
	return sc
}

// ---- listing ----

func parseListing(body []byte) ([]card, int) {
	s := string(body)
	start := strings.Index(s, `id="beeteam368_main-archive-page"`)
	if start < 0 {
		return nil, 0
	}
	end := strings.Index(s[start:], `id="main-sidebar"`)
	if end < 0 {
		end = len(s) - start
	}
	block := s[start : start+end]

	totalPages := 1
	if m := totalRe.FindStringSubmatch(block); m != nil {
		totalPages, _ = strconv.Atoi(m[1])
	}

	locs := articleRe.FindAllStringSubmatchIndex(block, -1)
	var cards []card
	for i, loc := range locs {
		chunkEnd := len(block)
		if i+1 < len(locs) {
			chunkEnd = locs[i+1][0]
		}
		chunk := block[loc[0]:chunkEnd]
		tm := titleLinkRe.FindStringSubmatch(chunk)
		if tm == nil {
			continue
		}
		c := card{
			id:    block[loc[2]:loc[3]],
			url:   tm[1],
			title: strings.TrimSpace(html.UnescapeString(tm[2])),
		}
		for _, m := range catClassRe.FindAllStringSubmatch(block[loc[4]:loc[5]], -1) {
			c.categories = append(c.categories, m[1])
		}
		if img := imgRe.FindString(chunk); img != "" {
			c.thumb = largestImage(img)
		}
		cards = append(cards, c)
	}
	return cards, totalPages
}

// largestImage picks the widest srcset candidate, falling back to src.
func largestImage(img string) string {
	best, bestW := "", 0
	if m := srcsetRe.FindStringSubmatch(img); m != nil {
		for _, cand := range strings.Split(m[1], ",") {
			f := strings.Fields(strings.TrimSpace(cand))
			if len(f) != 2 {
				continue
			}
			w, err := strconv.Atoi(strings.TrimSuffix(f[1], "w"))
			if err == nil && w > bestW {
				best, bestW = f[0], w
			}
		}
	}
	if best == "" {
		if m := srcRe.FindStringSubmatch(img); m != nil {
			best = m[1]
		}
	}
	return html.UnescapeString(best)
}

// ---- feed ----

type feedItem struct {
	title       string
	date        time.Time
	description string
}

type rss struct {
	Items []struct {
		Title       string `xml:"title"`
		PubDate     string `xml:"pubDate"`
		Link        string `xml:"link"`
		Description string `xml:"description"`
		Content     string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	} `xml:"channel>item"`
}

// feedIndex walks the archive's RSS feed lazily. The feed lists the same
// posts in the same order as the HTML archive, ten per page, so it is read
// only as far as the listing has reached. Items are keyed by link path: the
// feed's <guid> is unreliable (older posts carry a `?p=` or even a dev-host
// permalink instead of the post ID).
type feedIndex struct {
	s     *Scraper
	path  string
	delay time.Duration
	next  int
	done  bool
	items map[string]feedItem
}

// maxLookahead bounds how many feed pages one lookup may read past the
// listing. A post the feed omits would otherwise drag the whole feed in at
// once and leave every later lookup to an exhausted index.
const maxLookahead = 3

func (f *feedIndex) lookup(ctx context.Context, key string) (feedItem, error) {
	for fetched := 0; ; fetched++ {
		if it, ok := f.items[key]; ok {
			return it, nil
		}
		if f.done || fetched >= maxLookahead {
			scraper.Debugf(1, "%s: %s not found in feed", siteID, key)
			return feedItem{}, nil
		}
		f.next++
		if f.next > 1 && !scraper.Pace(ctx, f.delay) {
			return feedItem{}, ctx.Err()
		}
		feedURL := f.s.base + f.path + "feed/"
		if f.next > 1 {
			feedURL += "?paged=" + strconv.Itoa(f.next)
		}
		body, err := f.s.get(ctx, feedURL)
		if err != nil {
			if f.next > 1 && isNotFound(err) {
				f.done = true
				continue
			}
			return feedItem{}, err
		}
		items, err := parseFeed(body)
		if err != nil {
			return feedItem{}, scraper.ParseError(feedURL, err)
		}
		if len(items) == 0 {
			f.done = true
		}
		for k, v := range items {
			f.items[k] = v
		}
	}
}

func parseFeed(body []byte) (map[string]feedItem, error) {
	var doc rss
	if err := parseutil.DecodeXML(body, &doc); err != nil {
		return nil, fmt.Errorf("decoding feed: %w", err)
	}
	items := make(map[string]feedItem, len(doc.Items))
	for _, it := range doc.Items {
		key := linkKey(strings.TrimSpace(it.Link))
		if key == "" {
			continue
		}
		fi := feedItem{title: strings.TrimSpace(html.UnescapeString(it.Title))}
		if t, err := time.Parse(time.RFC1123Z, strings.TrimSpace(it.PubDate)); err == nil {
			fi.date = t.UTC()
		}
		desc := it.Content
		if strings.TrimSpace(desc) == "" {
			desc = it.Description
		}
		fi.description = cleanText(desc)
		items[key] = fi
	}
	return items, nil
}

// linkKey reduces a post URL to its path, so feed and listing agree
// whatever host or scheme each wrote.
func linkKey(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Path == "" {
		return ""
	}
	return strings.TrimSuffix(p.Path, "/")
}

func cleanText(s string) string {
	s = strings.NewReplacer("</p>", "\n", "<br>", "\n", "<br />", "\n").Replace(s)
	s = tagRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(html.UnescapeString(s), " ", " ")
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(spaceRe.ReplaceAllString(l, " "))
		if l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// ---- http ----

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func isNotFound(err error) bool {
	var se *httpx.StatusError
	return errors.As(err, &se) && se.StatusCode == http.StatusNotFound
}
