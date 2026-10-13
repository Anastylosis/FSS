// Package sensualpain scrapes sensualpain.com, whose videos are published as
// schema.org MusicRecording JSON-LD. See docs/scrapers.md.
package sensualpain

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "sensualpain"
	studioName    = "Sensual Pain"
	defaultBase   = "https://sensualpain.com"
	detailWorkers = 4
)

var (
	matchRe     = regexp.MustCompile(`^https?://(?:www\.)?sensualpain\.com(?:/|$)`)
	ldBlockRe   = regexp.MustCompile(`(?s)<script[^>]+type="application/ld\+json"[^>]*>(.*?)</script>`)
	fragmentRe  = regexp.MustCompile(`#(\w+)$`)
	slugStripRe = regexp.MustCompile(`[^a-z0-9]+`)
	ogImageRe   = regexp.MustCompile(`<meta property="og:image"\s+content="([^"]+)"`)
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(60 * time.Second), base: defaultBase}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"sensualpain.com", "sensualpain.com/tracks.php"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type item struct {
	id       string
	title    string
	date     time.Time
	duration int
}

type recording struct {
	Type          string `json:"@type"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	Description   string `json:"description"`
	DatePublished string `json:"datePublished"`
	Duration      string `json:"duration"`
	Keywords      string `json:"keywords"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	listURL := s.base + "/tracks.php"
	body, err := s.fetch(ctx, listURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	items := parseListing(body)
	if len(items) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(listURL, fmt.Errorf("no MusicRecording blocks"))))
		return
	}
	scraper.Debugf(1, "%s: %d items in listing", siteID, len(items))
	if !send(ctx, out, scraper.Progress(len(items))) {
		return
	}

	// The catalogue is newest-first, so everything before the first known
	// scene is new.
	fresh := items
	for i, it := range items {
		if opts.KnownIDs[it.id] {
			fresh = items[:i]
			break
		}
	}

	now := time.Now().UTC()
	scenes := make([]models.Scene, len(fresh))
	for i, it := range fresh {
		scenes[i] = s.baseScene(it, studioURL, now)
	}
	if !s.stream(ctx, scenes, opts, out) {
		return
	}
	if len(fresh) < len(items) {
		send(ctx, out, scraper.StoppedEarly())
	}
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func parseListing(body []byte) []item {
	var items []item
	for _, m := range ldBlockRe.FindAllSubmatch(body, -1) {
		var r recording
		if json.Unmarshal(m[1], &r) != nil || r.Type != "MusicRecording" {
			continue
		}
		frag := fragmentRe.FindStringSubmatch(r.URL)
		if frag == nil || r.Name == "" {
			continue
		}
		it := item{id: frag[1], title: html.UnescapeString(r.Name), duration: parseutil.ParseDurationColon(r.Duration)}
		if t, err := time.Parse("2006-01-02", r.DatePublished); err == nil {
			it.date = t.UTC()
		}
		items = append(items, it)
	}
	return items
}

func slugify(title string) string {
	return strings.Trim(slugStripRe.ReplaceAllString(strings.ToLower(title), "-"), "-")
}

func (s *Scraper) baseScene(it item, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     it.title,
		URL:       s.base + "/media.php?id=" + slugify(it.title),
		Date:      it.date,
		Duration:  it.duration,
		ScrapedAt: now,
	}
}

// stream fetches details on a worker pool and emits scenes in listing order
// as each becomes ready, so a cancelled run stops fetching.
func (s *Scraper) stream(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) bool {
	if len(scenes) == 0 {
		return true
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)

	ctx, cancel := context.WithCancel(ctx)

	done := make([]chan struct{}, len(scenes))
	for i := range done {
		done[i] = make(chan struct{})
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				s.fetchDetail(ctx, &scenes[i], opts, out)
				close(done[i])
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := range scenes {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	for i := range scenes {
		select {
		case <-done[i]:
		case <-ctx.Done():
			return false
		}
		if !send(ctx, out, scraper.Scene(scenes[i])) {
			return false
		}
	}
	return true
}

func (s *Scraper) fetchDetail(ctx context.Context, sc *models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if !scraper.Pace(ctx, opts.Delay) {
		return
	}
	body, err := s.fetch(ctx, sc.URL)
	if err != nil {
		scraper.Debugf(1, "%s: detail %s: %v", siteID, sc.URL, err)
		send(ctx, out, scraper.Error(err))
		return
	}
	applyDetail(sc, body)
}

func applyDetail(sc *models.Scene, body []byte) {
	for _, m := range ldBlockRe.FindAllSubmatch(body, -1) {
		var r recording
		if json.Unmarshal(m[1], &r) != nil || r.Type != "MusicRecording" {
			continue
		}
		sc.Description = html.UnescapeString(strings.TrimSpace(r.Description))
		for _, k := range strings.Split(r.Keywords, ",") {
			if k = strings.TrimSpace(html.UnescapeString(k)); k != "" {
				sc.Tags = append(sc.Tags, k)
			}
		}
		if sc.Duration == 0 {
			sc.Duration = parseutil.ParseDurationColon(r.Duration)
		}
		break
	}
	if m := ogImageRe.FindSubmatch(body); m != nil {
		sc.Thumbnail = html.UnescapeString(string(m[1]))
	}
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
