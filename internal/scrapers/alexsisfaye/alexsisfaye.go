// Package alexsisfaye scrapes alexsisfaye.com, a single-performer personal
// site on a custom CMS. See docs/scrapers.md.
package alexsisfaye

import (
	"context"
	"errors"
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

const (
	siteID     = "alexsisfaye"
	studioName = "Alexsis Faye"
	performer  = "Alexsis Faye"
	// detailWorkers caps the per-page detail pool; only the synopsis needs the
	// scene's own page.
	detailWorkers = 4
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?alexsisfaye\.com(?:/|$)`)

	// Cards are sliced at the wrapper that opens each one; the date precedes
	// the link, so splitting at the link would lose it.
	cardSplitRe = regexp.MustCompile(`<div class="boxed-clean">`)
	dateRe      = regexp.MustCompile(`(?s)added_and_likes.*?(\d{2}/\d{2}/\d{4})`)
	linkRe      = regexp.MustCompile(`<a href="/videos/([^"/?#]+)" class="photo-preview"\s+title="([^"]*)"`)
	thumbRe     = regexp.MustCompile(`background-image:\s*url\('([^']+)'\)`)
	durationRe  = regexp.MustCompile(`Full video:</strong>\s*(?:(\d+)\s*min)?\s*(?:(\d+)\s*sec)?`)

	descRe     = regexp.MustCompile(`(?s)<div class="media-description-content">(.*?)</div>`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

// Scraper implements scraper.StudioScraper for alexsisfaye.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Alexsis Faye scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://alexsisfaye.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"alexsisfaye.com",
		"alexsisfaye.com/videos",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type card struct {
	slug     string
	title    string
	date     time.Time
	duration int
	thumb    string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/videos?page=%d", s.base, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			// The last page is followed by a 404 rather than an empty page.
			var se *httpx.StatusError
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}

		fresh := cards[:0]
		for _, c := range cards {
			if seen[c.slug] {
				continue
			}
			seen[c.slug] = true
			fresh = append(fresh, c)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		enrichTo := len(fresh)
		for i, c := range fresh {
			if opts.KnownIDs[c.slug] {
				enrichTo = i
				break
			}
		}
		scenes := make([]models.Scene, len(fresh))
		for i, c := range fresh {
			scenes[i] = s.baseScene(c, studioURL, now)
		}
		s.enrich(ctx, scenes[:enrichTo], opts, out)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// parseListing reads the listing's scene cards.
func parseListing(body string) []card {
	marks := cardSplitRe.FindAllStringIndex(body, -1)
	cards := make([]card, 0, len(marks))
	for i, m := range marks {
		end := len(body)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		block := body[m[0]:end]

		link := linkRe.FindStringSubmatch(block)
		if link == nil {
			continue
		}
		c := card{slug: link[1], title: cleanText(link[2])}
		if d := dateRe.FindStringSubmatch(block); d != nil {
			if t, err := parseutil.TryParseDate(d[1], "02/01/2006"); err == nil {
				c.date = t
			}
		}
		if th := thumbRe.FindStringSubmatch(block); th != nil {
			c.thumb = html.UnescapeString(th[1])
		}
		if d := durationRe.FindStringSubmatch(block); d != nil {
			mins, _ := strconv.Atoi(d[1])
			secs, _ := strconv.Atoi(d[2])
			c.duration = mins*60 + secs
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:         c.slug,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     studioName,
		Title:      c.title,
		URL:        s.base + "/videos/" + c.slug,
		Date:       c.date,
		Duration:   c.duration,
		Performers: []string{performer},
		ScrapedAt:  now,
	}
	if c.thumb != "" {
		sc.Thumbnail = s.base + c.thumb
		if strings.HasPrefix(c.thumb, "http") {
			sc.Thumbnail = c.thumb
		}
	}
	return sc
}

// enrich fetches each scene's detail page for the synopsis.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(scenes) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range scenes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			if d := descRe.FindStringSubmatch(body); d != nil {
				scenes[i].Description = cleanText(d[1])
			}
		}(i)
	}
	wg.Wait()
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
