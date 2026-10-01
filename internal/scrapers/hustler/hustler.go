// Package hustler scrapes Hustler Unlimited (hustlerunlimited.com). The site
// runs WordPress with the Search & Filter plugin; its REST API is disabled, so
// the scraper walks the HTML `/videos/` listing (title, link, cover, channel)
// and fetches each title's page for the WordPress post ID, publish date and
// cast.
package hustler

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
	"github.com/Anastylosis/FSS/scraper"
)

var siteBase = "https://hustlerunlimited.com"

const defaultWorkers = 4

type Scraper struct {
	client *http.Client
}

func New() *Scraper { return &Scraper{client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "hustler" }
func (s *Scraper) Patterns() []string {
	return []string{
		"hustlerunlimited.com",
		"hustlerunlimited.com/videos",
		"hustlerunlimited.com/videos/?_sft_{taxonomy}={term}",
		"hustlerunlimited.com/model/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?hustlerunlimited\.com(?:/?$|/?\?|/videos/?(?:$|\?)|/model/[^/?#]+/?(?:$|\?))`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var modelPathRe = regexp.MustCompile(`^/model/([^/]+)/?$`)

// listingFilter turns the operator's URL into the Search & Filter query the
// /videos/ listing understands. A performer page maps onto the hu_actors
// filter, which also covers performers who have no page of their own.
func listingFilter(studioURL string) url.Values {
	q := url.Values{}
	u, err := url.Parse(studioURL)
	if err != nil {
		return q
	}
	if m := modelPathRe.FindStringSubmatch(u.Path); m != nil {
		q.Set("_sft_hu_actors", m[1])
		return q
	}
	for k, v := range u.Query() {
		if strings.HasPrefix(k, "_sft_") && len(v) > 0 {
			q[k] = v
		}
	}
	return q
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	filter := listingFilter(studioURL)
	if len(filter) > 0 {
		scraper.Debugf(1, "hustler: scraping filtered listing %s", filter.Encode())
	}

	now := time.Now().UTC()
	lastPage := 1
	scraper.Paginate(ctx, opts, "hustler", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := listingURL(filter, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}
		total := 0
		if page == 1 {
			lastPage = max(parseLastPage(body), 1)
			total = len(items)
			if lastPage > 1 {
				total = lastPage * len(items)
			}
		}

		scenes := s.fetchDetails(ctx, studioURL, items, opts, out, now)
		return scraper.PageResult{Scenes: scenes, Total: total, Continue: true, Done: page >= lastPage}, nil
	})
}

func listingURL(filter url.Values, page int) string {
	q := url.Values{}
	for k, v := range filter {
		q[k] = v
	}
	if page > 1 {
		q.Set("sf_paged", strconv.Itoa(page))
	}
	u := siteBase + "/videos/"
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	return u
}

// fetchDetails resolves each listing item through its own page, preserving the
// listing order. An item whose page fails is reported and dropped: without the
// post ID it cannot be keyed.
func (s *Scraper) fetchDetails(ctx context.Context, studioURL string, items []listItem, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) []models.Scene {
	workers := min(scraper.WorkerCount(opts, defaultWorkers), len(items))
	scraper.Debugf(1, "hustler: fetching %d details with %d workers", len(items), workers)

	results := make([]*models.Scene, len(items))
	errs := make([]error, len(items))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				sc, err := s.fetchDetail(ctx, studioURL, items[i], now)
				if err != nil {
					errs[i] = err
					continue
				}
				results[i] = &sc
			}
		}()
	}
feed:
	for i := range items {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	var scenes []models.Scene
	for i, sc := range results {
		if errs[i] != nil {
			select {
			case out <- scraper.Error(errs[i]):
			case <-ctx.Done():
				return scenes
			}
		}
		if sc != nil {
			scenes = append(scenes, *sc)
		}
	}
	return scenes
}

func (s *Scraper) fetchDetail(ctx context.Context, studioURL string, it listItem, now time.Time) (models.Scene, error) {
	fetchURL := it.url
	if u, err := url.Parse(it.url); err == nil {
		fetchURL = siteBase + u.Path
	}
	body, err := s.fetch(ctx, fetchURL)
	if err != nil {
		return models.Scene{}, err
	}
	d, err := parseDetail(body)
	if err != nil {
		return models.Scene{}, scraper.ParseError(it.url, err)
	}
	return toScene(studioURL, it, d, now), nil
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

type listItem struct {
	url       string
	title     string
	thumbnail string
	channel   string
}

var (
	itemCoverRe   = regexp.MustCompile(`class="coverlink" href="([^"]+)">([^<]*)</a>`)
	itemImgRe     = regexp.MustCompile(`<img src="([^"]+)"`)
	itemChannelRe = regexp.MustCompile(`(?s)<a href="/videos/\?_sft_video_channels=[^"]*">\s*(?:<i[^>]*></i>)?\s*([^<]*?)\s*</a>`)
	lastPageRe    = regexp.MustCompile(`sf_paged=(\d+)`)
)

func parseListing(body []byte) []listItem {
	chunks := strings.Split(string(body), `<div class="__item">`)
	var items []listItem
	for _, c := range chunks[1:] {
		m := itemCoverRe.FindStringSubmatch(c)
		if m == nil {
			continue
		}
		it := listItem{
			url:   m[1],
			title: strings.TrimSpace(html.UnescapeString(m[2])),
		}
		if im := itemImgRe.FindStringSubmatch(c); im != nil {
			it.thumbnail = absURL(im[1])
		}
		if cm := itemChannelRe.FindStringSubmatch(c); cm != nil {
			it.channel = channelName(html.UnescapeString(cm[1]))
		}
		items = append(items, it)
	}
	return items
}

func parseLastPage(body []byte) int {
	n := 0
	for _, m := range lastPageRe.FindAllSubmatch(body, -1) {
		if v, err := strconv.Atoi(string(m[1])); err == nil && v > n {
			n = v
		}
	}
	return n
}

// channelName de-shouts the listing's all-caps "HUSTLER" badge so it matches
// the mixed-case channel names ("Barely Legal") beside it.
func channelName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.ToUpper(s) != s {
		return s
	}
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func absURL(src string) string {
	if strings.HasPrefix(src, "/") && !strings.HasPrefix(src, "//") {
		return "https://hustlerunlimited.com" + src
	}
	return src
}

type detail struct {
	id         string
	date       time.Time
	performers []string
}

var (
	postIDRe    = regexp.MustCompile(`\bpostid-(\d+)\b`)
	publishedRe = regexp.MustCompile(`"datePublished":"([^"]+)"`)
	performerRe = regexp.MustCompile(`<h5 class="fs-heading-6">([^<]+)</h5>`)
)

func parseDetail(body []byte) (detail, error) {
	var d detail
	m := postIDRe.FindSubmatch(body)
	if m == nil {
		return d, fmt.Errorf("no post ID on title page")
	}
	d.id = string(m[1])
	if pm := publishedRe.FindSubmatch(body); pm != nil {
		if t, err := time.Parse(time.RFC3339, string(pm[1])); err == nil {
			d.date = t.UTC()
		}
	}

	// The cast block precedes the "More Like This" carousel; bounding it keeps
	// related titles' headings out of the performer list.
	text := string(body)
	if i := strings.Index(text, "pornstars-wrapper"); i >= 0 {
		text = text[i:]
		if j := strings.Index(text, "More Like This"); j >= 0 {
			text = text[:j]
		}
		for _, pm := range performerRe.FindAllStringSubmatch(text, -1) {
			if name := strings.TrimSpace(html.UnescapeString(pm[1])); name != "" {
				d.performers = append(d.performers, name)
			}
		}
	}
	return d, nil
}

func toScene(studioURL string, it listItem, d detail, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         d.id,
		SiteID:     "hustler",
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        it.url,
		Date:       d.date,
		Thumbnail:  it.thumbnail,
		Performers: d.performers,
		Studio:     "Hustler",
		ScrapedAt:  now,
	}
	if it.channel != "" {
		scene.Series = it.channel
		scene.Categories = []string{it.channel}
	}
	return scene
}
