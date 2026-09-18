// Package ericvideos scrapes Eric Videos (ericvideos.com). The catalogue is
// behind an age gate, and cast is only discoverable from the actor index.
// See docs/scrapers.md.
package ericvideos

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "ericvideos"
	studioName = "Eric Videos"
	siteBase   = "https://www.ericvideos.com"
	// gatePath accepts the age warning and sets the session cookie.
	gatePath       = "/warning-all.php?accept=ok&lang=EN&retour=%2F"
	defaultWorkers = 4
	maxPages       = 200
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?ericvideos\.com(?:/|$)`)
	cardRe     = regexp.MustCompile(`<div class="vod-item[^"]*"`)
	linkRe     = regexp.MustCompile(`/EN/vod/(\d+)/(\d+)/([a-z0-9-]+)`)
	titleRe    = regexp.MustCompile(`(?s)<div class="video_titre">\s*<h3>(.*?)</h3>`)
	thumbRe    = regexp.MustCompile(`<img[^>]+src="(/medias-cache/[^"]+)"`)
	durationRe = regexp.MustCompile(`class="duree"[^>]*>.*?(\d+)\s*min`)
	actorRe    = regexp.MustCompile(`/EN/acteurs/(\d+)/([^"]+)"`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	client *http.Client
	base   string
	gate   sync.Once
}

func New() *Scraper {
	jar, _ := cookiejar.New(nil)
	c := httpx.NewClient(30 * time.Second)
	c.Jar = jar
	return &Scraper{client: c, base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"ericvideos.com", "ericvideos.com/EN/vod/1/page{N}"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type card struct {
	id, slug, title, thumb, description string
	duration                            int
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	s.gate.Do(func() { s.enter(ctx) })

	cards, err := s.walkListing(ctx, opts)
	if err != nil {
		send(ctx, out, scraper.Error(err))
	}
	if len(cards) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(s.base, fmt.Errorf("no video cards in the listing"))))
		return
	}
	if !send(ctx, out, scraper.Progress(len(cards))) {
		return
	}

	cast := s.castByVideo(ctx, opts)
	now := time.Now().UTC()
	for _, c := range cards {
		scene := models.Scene{
			ID:          c.id,
			SiteID:      siteID,
			StudioURL:   studioURL,
			Studio:      studioName,
			Title:       c.title,
			URL:         fmt.Sprintf("%s/EN/vod/1/%s/%s", siteBase, c.id, c.slug),
			Description: c.description,
			Duration:    c.duration,
			Performers:  cast[c.id],
			ScrapedAt:   now,
		}
		if c.thumb != "" {
			scene.Thumbnail = siteBase + c.thumb
		}
		if !send(ctx, out, scraper.Scene(scene)) {
			return
		}
	}
}

// enter accepts the age warning, which every page redirects to until the
// session cookie is held.
func (s *Scraper) enter(ctx context.Context) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     s.base + gatePath,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		scraper.Debugf(1, "ericvideos: age gate: %v", err)
		return
	}
	_ = resp.Body.Close()
	scraper.Debugf(1, "ericvideos: age gate accepted")
}

func (s *Scraper) walkListing(ctx context.Context, opts scraper.ListOpts) ([]card, error) {
	var cards []card
	seen := map[string]bool{}
	for page := 1; page <= maxPages; page++ {
		if ctx.Err() != nil {
			return cards, nil
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return cards, nil
			}
		}
		body, err := s.fetchPage(ctx, fmt.Sprintf("%s/EN/vod/1/page%d", s.base, page))
		if err != nil {
			return cards, fmt.Errorf("page %d: %w", page, err)
		}
		found := parseCards(body)
		fresh := 0
		for _, c := range found {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			cards = append(cards, c)
			fresh++
		}
		scraper.Debugf(1, "ericvideos: page %d, %d new videos", page, fresh)
		if fresh == 0 {
			return cards, nil
		}
	}
	return cards, nil
}

// parseCards reads the listing cards, which carry everything the detail page
// does — title, runtime, description and still.
func parseCards(body string) []card {
	starts := cardRe.FindAllStringIndex(body, -1)
	cards := make([]card, 0, len(starts))
	for i, loc := range starts {
		end := len(body)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := body[loc[0]:end]

		m := linkRe.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		c := card{id: m[2], slug: m[3]}
		if t := titleRe.FindStringSubmatch(block); t != nil {
			c.title = cleanText(t[1])
		}
		if c.title == "" {
			c.title = strings.ReplaceAll(c.slug, "-", " ")
		}
		if th := thumbRe.FindStringSubmatch(block); th != nil {
			c.thumb = th[1]
		}
		if d := durationRe.FindStringSubmatch(block); d != nil {
			if mins := atoi(d[1]); mins > 0 {
				c.duration = mins * 60
			}
		}
		if desc := descriptionFor(body, c.id); desc != "" {
			c.description = desc
		}
		cards = append(cards, c)
	}
	return cards
}

// descriptionFor reads the hidden description block, which the theme renders
// outside the card it belongs to.
func descriptionFor(body, id string) string {
	re := regexp.MustCompile(fmt.Sprintf(`(?s)<div id="desc_%s" class="desc[^"]*"><div class="text">(.*?)</div>`, regexp.QuoteMeta(id)))
	if m := re.FindStringSubmatch(body); m != nil {
		return cleanText(m[1])
	}
	return ""
}

// castByVideo maps video id to performers by walking the actor index: the
// listing and the detail page both name the cast nowhere.
func (s *Scraper) castByVideo(ctx context.Context, opts scraper.ListOpts) map[string][]string {
	cast := map[string][]string{}
	body, err := s.fetchPage(ctx, s.base+"/EN/acteurs/")
	if err != nil {
		return cast
	}
	actors := map[string]string{}
	for _, m := range actorRe.FindAllStringSubmatch(body, -1) {
		actors[m[1]] = cleanText(strings.ReplaceAll(m[2], "+", " "))
	}
	scraper.Debugf(1, "ericvideos: resolving cast from %d actor pages", len(actors))

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	type job struct{ id, name string }
	jobs := make(chan job)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				page, err := s.fetchPage(ctx, fmt.Sprintf("%s/EN/acteurs/%s/%s", s.base, j.id, j.name))
				if err != nil {
					continue
				}
				mu.Lock()
				for _, m := range linkRe.FindAllStringSubmatch(page, -1) {
					cast[m[2]] = appendUnique(cast[m[2]], j.name)
				}
				mu.Unlock()
			}
		}()
	}
	for id, name := range actors {
		select {
		case jobs <- job{id: id, name: name}:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return cast
		}
	}
	close(jobs)
	wg.Wait()
	return cast
}

func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if strings.EqualFold(existing, v) {
			return list
		}
	}
	return append(list, v)
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) (string, error) {
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

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}
