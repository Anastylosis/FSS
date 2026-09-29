package alternadudes

import (
	"context"
	"fmt"
	"html"
	"net/http"
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

type Scraper struct {
	client *http.Client
	// base is the site root used for *fetches*, a field so offline tests can
	// redirect them. Output fields (Scene.URL, Thumbnail) keep the const: a
	// scene's public address must not become a 127.0.0.1 URL because a test
	// happened to fetch it from there.
	base string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "alternadudes" }

func (s *Scraper) Patterns() []string {
	return []string{
		"alternadudes.com",
		"alternadudes.com/models/{name}.html",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?alternadudes\.com(?:/|$)`)
	modelRe = regexp.MustCompile(`/models/[^/]+\.html`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

const siteBase = "https://www.alternadudes.com"

var (
	cardRe    = regexp.MustCompile(`(?s)<div class="item col-lg-4[^"]*">\s*<div class="product-item">.*?</div>\s*</div>\s*</div>`)
	thumbRe   = regexp.MustCompile(`<img src="(/content/contentthumbs/[^"]+)"`)
	titleRe   = regexp.MustCompile(`<img[^>]+alt="([^"]+)"`)
	trailerRe = regexp.MustCompile(`href="(?:https?://(?:www\.)?alternadudes\.com)?(/trailers/[^"]+\.html)"`)
	contentID = regexp.MustCompile(`/(\d+)-\d+x\.jpg`)
	maxPageRe = regexp.MustCompile(`movies_(\d+)_d\.html`)

	detailDescRe = regexp.MustCompile(`(?s)<h4>([^<]+)</h4>`)
	detailTagsRe = regexp.MustCompile(`<meta\s+name="keywords"\s+content="([^"]*)"`)
	baseURLRe    = regexp.MustCompile(`^(https?://[^/]+)`)
)

type listEntry struct {
	id         string
	title      string
	thumbnail  string
	trailerURL string
	// listURL is the page the card was found on, used as the scene URL when the
	// card carries no trailer anchor — it is then the only page the scene is
	// reachable at, and Scene.URL must never be empty.
	listURL string
}

func parseListingPage(body []byte, listURL string) []listEntry {
	cards := cardRe.FindAll(body, -1)
	entries := make([]listEntry, 0, len(cards))
	for _, card := range cards {
		block := string(card)

		e := listEntry{listURL: listURL}

		if m := contentID.FindStringSubmatch(block); m != nil {
			e.id = m[1]
		}
		if e.id == "" {
			continue
		}

		if m := titleRe.FindStringSubmatch(block); m != nil {
			e.title = html.UnescapeString(strings.TrimSpace(m[1]))
		}

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			e.thumbnail = siteBase + m[1]
		}

		if m := trailerRe.FindStringSubmatch(block); m != nil {
			e.trailerURL = m[1]
		}

		entries = append(entries, e)
	}
	return entries
}

func estimateTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

func pageURL(base string, page int) string {
	if page <= 1 {
		return base + "/categories/movies.html"
	}
	return fmt.Sprintf("%s/categories/movies_%d_d.html", base, page)
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := s.base
	if strings.HasPrefix(studioURL, "http") {
		if m := baseURLRe.FindString(studioURL); m != "" {
			base = m
		}
	}

	work := make(chan listEntry, opts.Workers)
	var wg sync.WaitGroup

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene := s.processEntry(ctx, base, studioURL, entry)
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	if modelRe.MatchString(studioURL) {
		s.produceModel(ctx, studioURL, opts, out, work)
	} else {
		s.produceListing(ctx, base, opts, out, work)
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) produceModel(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listEntry) {
	body, err := s.fetchPage(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	entries := parseListingPage(body, studioURL)
	if len(entries) > 0 {
		select {
		case out <- scraper.Progress(len(entries)):
		case <-ctx.Done():
			return
		}
	}
	seen := make(map[string]bool)
	for _, e := range entries {
		if seen[e.id] {
			continue
		}
		seen[e.id] = true
		if opts.KnownIDs[e.id] {
			continue
		}
		select {
		case work <- e:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) produceListing(ctx context.Context, base string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listEntry) {
	seen := make(map[string]bool)
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			break
		}
		scraper.Debugf(1, "alternadudes: fetching page %d", page)

		listURL := pageURL(base, page)
		body, err := s.fetchPage(ctx, listURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		entries := parseListingPage(body, listURL)
		if len(entries) == 0 {
			break
		}

		if page == 1 {
			total := estimateTotal(body, len(entries))
			if total > 0 {
				scraper.Debugf(1, "alternadudes: %d total scenes", total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
				}
			}
		}

		cancelled := false
		hitKnown := false
		for _, e := range entries {
			if seen[e.id] {
				continue
			}
			seen[e.id] = true
			if opts.KnownIDs[e.id] {
				hitKnown = true
				break
			}
			select {
			case work <- e:
			case <-ctx.Done():
				cancelled = true
			}
			if cancelled {
				break
			}
		}
		if cancelled || hitKnown {
			if hitKnown {
				scraper.Debugf(1, "alternadudes: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
			}
			break
		}
	}
}

func (s *Scraper) processEntry(ctx context.Context, base, studioURL string, entry listEntry) models.Scene {
	now := time.Now().UTC()
	scene := models.Scene{
		ID:        entry.id,
		SiteID:    "alternadudes",
		StudioURL: studioURL,
		Title:     entry.title,
		Thumbnail: entry.thumbnail,
		Studio:    "AlternaDudes",
		ScrapedAt: now,
	}

	scene.URL = entry.listURL + "#" + entry.id
	if entry.trailerURL != "" {
		scene.URL = base + entry.trailerURL
		body, err := s.fetchPage(ctx, scene.URL)
		if err == nil {
			if m := detailDescRe.FindSubmatch(body); m != nil {
				scene.Description = strings.TrimSpace(html.UnescapeString(string(m[1])))
			}
			if m := detailTagsRe.FindSubmatch(body); m != nil {
				for _, tag := range strings.Split(html.UnescapeString(string(m[1])), ",") {
					tag = strings.TrimSpace(tag)
					if tag != "" && tag != "..." {
						scene.Tags = append(scene.Tags, tag)
					}
				}
			}
			og := parseutil.OpenGraph(body)
			if v := og["og:image"]; v != "" {
				scene.Thumbnail = html.UnescapeString(v)
			}
		}
	}

	return scene
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
