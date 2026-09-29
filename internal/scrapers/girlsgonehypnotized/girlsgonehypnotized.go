package girlsgonehypnotized

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

const (
	defaultBase = "https://girlsgonehypnotized.com"
	siteID      = "girlsgonehypnotized"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?girlsgonehypnotized\.com(?:/|$)`)

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{
		"girlsgonehypnotized.com",
		"girlsgonehypnotized.com/{Slug}.html",
	}
}
func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func resolveBase(studioURL string) string {
	for _, prefix := range []string{"https://www.", "https://", "http://www.", "http://"} {
		if strings.HasPrefix(studioURL, prefix) {
			if idx := strings.Index(studioURL[len(prefix):], "/"); idx >= 0 {
				return studioURL[:len(prefix)+idx]
			}
			return studioURL
		}
	}
	return defaultBase
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := resolveBase(studioURL)

	scraper.Debugf(1, "%s: fetching homepage", siteID)
	body, err := s.fetchPage(ctx, base+"/")
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("homepage: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	entries := parseHomepage(body, base)
	scraper.Debugf(1, "%s: found %d videos on homepage", siteID, len(entries))

	if len(entries) == 0 {
		return
	}

	select {
	case out <- scraper.Progress(len(entries)):
	case <-ctx.Done():
		return
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan homepageEntry)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				scene, err := s.fetchDetail(ctx, base, entry, studioURL, opts.Delay)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)
		// The entry list comes from one already-fetched homepage, so stopping
		// at a known entry saves no listing request and hides every entry after
		// it. Skip the stored ones; the detail fetch each would have cost is
		// still saved.
		skipped := 0
		for _, entry := range entries {
			if opts.KnownIDs[entry.id] {
				skipped++
				continue
			}
			select {
			case work <- entry:
			case <-ctx.Done():
				return
			}
		}
		if skipped > 0 {
			scraper.Debugf(1, "%s: skipped %d already-stored entr(ies)", siteID, skipped)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
		}
	}()

	wg.Wait()
}

type homepageEntry struct {
	id    string
	url   string
	title string
	thumb string
}

var linkRe = regexp.MustCompile(`(?s)<a[^>]+href="([A-Z][A-Za-z0-9]+\.html)"[^>]*>\s*<img[^>]+src="([^"]+)"[^>]+alt="([^"]*)"`)

var excludeRe = regexp.MustCompile(`(?i)GirlSearch|index`)

func parseHomepage(body []byte, base string) []homepageEntry {
	matches := linkRe.FindAllSubmatch(body, -1)
	seen := make(map[string]bool, len(matches))
	entries := make([]homepageEntry, 0, len(matches))

	for _, m := range matches {
		href := string(m[1])
		if excludeRe.MatchString(href) {
			continue
		}

		slug := strings.TrimSuffix(href, ".html")
		if seen[slug] {
			continue
		}
		seen[slug] = true

		thumbSrc := string(m[2])
		if !strings.HasPrefix(thumbSrc, "http") {
			thumbSrc = base + "/" + thumbSrc
		}
		thumbURL, err := url.PathUnescape(thumbSrc)
		if err != nil {
			thumbURL = thumbSrc
		}

		entries = append(entries, homepageEntry{
			id:    slug,
			url:   base + "/" + href,
			title: string(m[3]),
			thumb: thumbURL,
		})
	}
	return entries
}

var (
	titleRe    = regexp.MustCompile(`<title>Girls Gone Hypnotized - (.+?)</title>`)
	durationRe = regexp.MustCompile(`(\d+)\s+minutes?,\s*(\d+)\s+seconds?`)
	priceRe    = regexp.MustCompile(`Only \$(\d+\.\d+)`)
)

type detailData struct {
	title    string
	duration int
	price    float64
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := titleRe.FindSubmatch(body); m != nil {
		d.title = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := durationRe.FindSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(string(m[1]))
		secs, _ := strconv.Atoi(string(m[2]))
		d.duration = mins*60 + secs
	}

	if m := priceRe.FindSubmatch(body); m != nil {
		d.price, _ = strconv.ParseFloat(string(m[1]), 64)
	}

	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, _ string, entry homepageEntry, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:        entry.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     entry.title,
		URL:       entry.url,
		Thumbnail: entry.thumb,
		Studio:    "GG Fetish Media",
		ScrapedAt: now,
	}

	body, err := s.fetchPage(ctx, entry.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", entry.id, err)
	}

	detail := parseDetailPage(body)
	if detail.title != "" {
		scene.Title = detail.title
	}
	scene.Duration = detail.duration
	if detail.price > 0 {
		scene.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: detail.price,
		})
	}

	return scene, nil
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
