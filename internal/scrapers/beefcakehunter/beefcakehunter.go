// Package beefcakehunter scrapes Beefcake Hunter (beefcakehunter.com), a
// WordPress site whose scenes are a custom "video" post type with a
// "video-category" taxonomy that mixes performer first names and keywords.
package beefcakehunter

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID         = "beefcakehunter"
	studioName     = "Beefcake Hunter"
	defaultWorkers = 3
)

var siteBase = "https://beefcakehunter.com"

// Scraper implements scraper.StudioScraper for Beefcake Hunter.
type Scraper struct {
	Client *http.Client
}

// New constructs a Beefcake Hunter scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"beefcakehunter.com",
		"beefcakehunter.com/home/",
		"beefcakehunter.com/video-category/{slug}/",
		"beefcakehunter.com/video-category/keywords/{slug}/",
	}
}

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?beefcakehunter\.com(?:[/?#]|$)`)
	categoryRe = regexp.MustCompile(`^https?://(?:www\.)?beefcakehunter\.com/(video-category/(?:keywords/)?[a-z0-9-]+)/?`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// pageURL returns the listing URL for one page. The main catalogue is the
// post-type query (`/?post_type=video`), which the theme renders as a
// paginated archive even though the post type has no archive slug of its own.
func pageURL(studioURL string, page int) string {
	if m := categoryRe.FindStringSubmatch(studioURL); m != nil {
		if page == 1 {
			return fmt.Sprintf("%s/%s/", siteBase, m[1])
		}
		return fmt.Sprintf("%s/%s/page/%d/", siteBase, m[1], page)
	}
	if page == 1 {
		return siteBase + "/?post_type=video"
	}
	return fmt.Sprintf("%s/page/%d/?post_type=video", siteBase, page)
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if categoryRe.MatchString(studioURL) {
		scraper.Debugf(1, "%s: scraping category page", siteID)
	}
	workers := scraper.WorkerCount(opts, defaultWorkers)
	now := time.Now().UTC()
	var lastDate time.Time

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := pageURL(studioURL, page)
		body, err := s.fetch(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := parseListing(string(body), &lastDate)
		if len(items) == 0 && page == 1 {
			return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("no videos on listing page"))
		}

		scenes := make([]models.Scene, len(items))
		for i, it := range items {
			scenes[i] = it.toScene(studioURL, now)
		}
		s.enrich(ctx, opts, workers, scenes, out)
		return scraper.PageResult{Scenes: scenes, Done: !hasNextPage(string(body))}, nil
	})
}

// enrich fetches each new scene's detail page for the full description,
// exact date and preview. A failed detail fetch keeps the listing data and
// reports the error, since the description is then only an excerpt.
func (s *Scraper) enrich(ctx context.Context, opts scraper.ListOpts, workers int, scenes []models.Scene, out chan<- scraper.SceneResult) {
	work := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					continue
				}
				body, err := s.fetch(ctx, scenes[i].URL)
				if err != nil {
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", scenes[i].URL, err)):
					case <-ctx.Done():
					}
					continue
				}
				applyDetail(&scenes[i], string(body))
			}
		}()
	}
	for i := range scenes {
		if opts.KnownIDs[scenes[i].ID] {
			continue
		}
		select {
		case work <- i:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(work)
	wg.Wait()
}

// ---- listing ----

type listItem struct {
	id, url, title, excerpt, thumbnail string
	date                               time.Time
	slugs                              []string
}

var (
	cardSplitRe   = regexp.MustCompile(`<div class="t7-video t7-video-loop `)
	cardClassRe   = regexp.MustCompile(`^post-(\d+)([^"]*)"`)
	cardTitleRe   = regexp.MustCompile(`(?s)<h3 class="t7-video-title"><a href="([^"]+)">(.*?)</a>`)
	cardDateRe    = regexp.MustCompile(`<span class="t7-video-date">([^<]*)</span>`)
	cardThumbRe   = regexp.MustCompile(`(?s)class="t7-video-thumbnail".*?<img[^>]+src="([^"]+)"`)
	cardExcerptRe = regexp.MustCompile(`(?s)<div class="t7-video-excerpt">(.*?)</div>`)
	readMoreRe    = regexp.MustCompile(`(?s)<a class="t7-post-readmore".*?</a>`)
	categorySlug  = regexp.MustCompile(`\bvideo-category-([a-z0-9-]+)`)
	nextPageRe    = regexp.MustCompile(`class="next page-numbers"`)
)

// parseListing extracts the archive cards. WordPress's the_date() prints a
// date only for the first post of each day, so a blank date inherits the
// previous card's — carried across pages through lastDate.
func parseListing(body string, lastDate *time.Time) []listItem {
	parts := cardSplitRe.Split(body, -1)
	var items []listItem
	for _, p := range parts[1:] {
		m := cardClassRe.FindStringSubmatch(p)
		if m == nil {
			continue
		}
		it := listItem{id: m[1]}
		for _, c := range categorySlug.FindAllStringSubmatch(m[2], -1) {
			it.slugs = append(it.slugs, c[1])
		}
		if t := cardTitleRe.FindStringSubmatch(p); t != nil {
			it.url = t[1]
			it.title = cleanText(t[2])
		}
		if it.url == "" {
			continue
		}
		if t := cardThumbRe.FindStringSubmatch(p); t != nil {
			it.thumbnail = t[1]
		}
		if t := cardExcerptRe.FindStringSubmatch(p); t != nil {
			it.excerpt = cleanText(readMoreRe.ReplaceAllString(t[1], ""))
		}
		if t := cardDateRe.FindStringSubmatch(p); t != nil {
			if d, err := time.Parse("January 2, 2006", strings.TrimSpace(t[1])); err == nil {
				*lastDate = d
			}
		}
		it.date = *lastDate
		items = append(items, it)
	}
	return items
}

func hasNextPage(body string) bool { return nextPageRe.MatchString(body) }

func (it listItem) toScene(studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          it.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       it.title,
		URL:         it.url,
		Date:        it.date,
		Description: it.excerpt,
		Thumbnail:   it.thumbnail,
		Studio:      studioName,
		ScrapedAt:   now,
	}
	sc.Performers, sc.Tags = classify(it.slugs)
	return sc
}

// tagNames lists every video-category slug that is not a performer. The
// taxonomy holds performer first names and keywords side by side and the
// body classes carry only slugs, so the split has to be named here; an
// unlisted slug is taken to be a performer, which is what new categories have
// been in practice.
var tagNames = map[string]string{
	"anal": "Anal", "anonymous": "Anonymous", "arabic": "Arabic", "asian": "Asian",
	"bbc-keywords": "BBC", "beefcakes": "", "black": "Black", "bwc": "BWC",
	"construction-worker": "Construction Worker", "doggy": "Doggy", "dominant": "Dominant",
	"edging": "Edging", "european": "European", "face-fucking": "Face Fucking",
	"facial": "Facial", "feet": "Feet", "glory-hole": "Glory Hole", "group-sex": "Group Sex",
	"hairy": "Hairy", "hairy-2": "Hairy", "hung": "Hung", "keywords": "", "kiss": "Kiss",
	"latino": "Latino", "married": "Married", "missionary": "Missionary", "mixed": "Mixed",
	"moaning": "Moaning", "muscle": "Muscle", "oral": "Oral", "pov": "POV", "riding": "Riding",
	"rimming": "Rimming", "shower": "Shower", "solo": "Solo", "swallow": "Swallow",
	"verbal": "Verbal", "white": "White",
}

func classify(slugs []string) (performers, tags []string) {
	seen := map[string]bool{}
	for _, slug := range slugs {
		if name, ok := tagNames[slug]; ok {
			if name != "" && !seen[name] {
				seen[name] = true
				tags = append(tags, name)
			}
			continue
		}
		performers = append(performers, slugToName(slug))
	}
	return performers, tags
}

func slugToName(slug string) string {
	words := strings.Split(slug, "-")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// ---- detail ----

var (
	detailTitleRe = regexp.MustCompile(`(?s)<h2 class="sunday-funday-video-title">(.*?)</h2>`)
	detailDateRe  = regexp.MustCompile(`(?s)<span class="video-ui-date">\s*(\d{2}/\d{2}/\d{4})`)
	posterRe      = regexp.MustCompile(`<video class="wp-video-shortcode"[^>]*\sposter="([^"]+)"`)
	previewRe     = regexp.MustCompile(`<source type="video/mp4" src="([^"?]+)`)
	contentRe     = regexp.MustCompile(`(?s)<div class="t7-video-content">(.*?)</div>\s*</div>\s*</div>\s*</div>`)
)

func applyDetail(sc *models.Scene, body string) {
	if m := detailTitleRe.FindStringSubmatch(body); m != nil {
		if t := cleanText(m[1]); t != "" {
			sc.Title = t
		}
	}
	if m := detailDateRe.FindStringSubmatch(body); m != nil {
		if d, err := time.Parse("01/02/2006", m[1]); err == nil {
			sc.Date = d
		}
	}
	if m := posterRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = m[1]
	}
	if m := previewRe.FindStringSubmatch(body); m != nil {
		sc.Preview = m[1]
	}
	if m := contentRe.FindStringSubmatch(body); m != nil {
		if d := cleanText(m[1]); d != "" {
			sc.Description = d
		}
	}
}

// ---- text ----

var (
	breakRe = regexp.MustCompile(`(?i)<br\s*/?>|</p>`)
	tagRe   = regexp.MustCompile(`<[^>]+>`)
	spaceRe = regexp.MustCompile(`[ \t\r\f]+`)
	nlRe    = regexp.MustCompile(`\s*\n\s*`)
)

func cleanText(s string) string {
	s = breakRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = spaceRe.ReplaceAllString(s, " ")
	s = nlRe.ReplaceAllString(s, "\n")
	return strings.TrimSpace(s)
}

// ---- HTTP ----

func (s *Scraper) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
