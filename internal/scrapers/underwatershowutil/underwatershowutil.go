// Package underwatershowutil scrapes the RevshareCash "jscroll" sites
// (Underwater Show, Anal-Coach). The public tour lazy-loads its gallery via an
// AJAX endpoint (load_pics.php) that returns a chunk of six
// <figure class="one-picture"> blocks per request, paged by a ?skip= offset
// that steps by six until the endpoint returns an empty body. Each block
// carries a thumbnail, a performer name, and an optional story description.
// There are no dates, durations, or per-scene pages.
package underwatershowutil

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// pageStep is the fixed number of figures returned per AJAX request.
const pageStep = 6

// maxSkip is a safety ceiling so a server that never returns an empty body
// can't spin the loop forever.
const maxSkip = 100000

type SiteConfig struct {
	ID       string
	Studio   string
	SiteBase string // e.g. "https://underwatershow.com" — no trailing slash
	LoadPath string // e.g. "load_pics.php" or "modules/load_pics.php"
	Patterns []string
	MatchRe  *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		Client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string               { return s.cfg.ID }
func (s *Scraper) Patterns() []string       { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool { return s.cfg.MatchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	figureSplitRe = regexp.MustCompile(`<figure[^>]*class="one-picture"`)
	imgSrcRe      = regexp.MustCompile(`<img[^>]*\ssrc="([^"]+)"`)
	girlnameRe    = regexp.MustCompile(`(?s)<p class="girlname">(.*?)</p>`)
	storytextRe   = regexp.MustCompile(`(?s)<p class="storytext">(.*?)</p>`)
	readmoreARe   = regexp.MustCompile(`(?s)<a[^>]*readmore_link[^>]*>.*?</a>`)
	readmoreSpan  = regexp.MustCompile(`(?s)<span[^>]*readmore_link[^>]*>.*?</span>`)
	tagRe         = regexp.MustCompile(`<[^>]+>`)
	wsRe          = regexp.MustCompile(`\s+`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	for skip := 0; skip <= maxSkip; skip += pageStep {
		if ctx.Err() != nil {
			return
		}
		if skip > 0 && !scraper.Pace(ctx, opts.Delay) {
			return
		}

		pageURL := fmt.Sprintf("%s/%s?skip=%d", s.cfg.SiteBase, s.cfg.LoadPath, skip)
		scraper.Debugf(1, "%s: fetching skip=%d (%s)", s.cfg.ID, skip, pageURL)
		blocks, err := s.fetchBlocks(ctx, pageURL)
		if err != nil {
			s.send(ctx, out, scraper.Error(fmt.Errorf("skip %d: %w", skip, err)))
			return
		}
		if len(blocks) == 0 {
			scraper.Debugf(1, "%s: empty page at skip=%d, stopping", s.cfg.ID, skip)
			return
		}
		for _, b := range blocks {
			if sc, ok := s.toScene(studioURL, b, now); ok {
				s.send(ctx, out, scraper.Scene(sc))
			}
		}
	}
}

func (s *Scraper) fetchBlocks(ctx context.Context, pageURL string) ([]string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	body, err := func() ([]byte, error) {
		defer func() { _ = resp.Body.Close() }()
		return httpx.ReadBody(resp.Body)
	}()
	if err != nil {
		return nil, err
	}
	parts := figureSplitRe.Split(string(body), -1)
	if len(parts) <= 1 {
		return nil, nil
	}
	return parts[1:], nil
}

func (s *Scraper) toScene(studioURL, block string, now time.Time) (models.Scene, bool) {
	m := imgSrcRe.FindStringSubmatch(block)
	if m == nil {
		return models.Scene{}, false
	}
	src := m[1]
	id := stem(src)
	if id == "" {
		return models.Scene{}, false
	}

	scene := models.Scene{
		ID:        id,
		SiteID:    s.cfg.ID,
		StudioURL: studioURL,
		URL:       s.cfg.SiteBase,
		Thumbnail: absURL(s.cfg.SiteBase, src),
		Studio:    s.cfg.Studio,
		ScrapedAt: now,
	}

	var girl string
	if g := girlnameRe.FindStringSubmatch(block); g != nil {
		girl = cleanText(g[1])
	}
	if girl != "" {
		scene.Performers = []string{girl}
	}
	if st := storytextRe.FindStringSubmatch(block); st != nil {
		scene.Description = cleanStory(st[1])
	}

	switch {
	case girl != "":
		scene.Title = girl
	case scene.Description != "":
		scene.Title = snippet(scene.Description, 60)
	default:
		scene.Title = id
	}
	return scene, true
}

// stem returns the filename without directory or extension.
func stem(src string) string {
	if src == "" {
		return ""
	}
	base := path.Base(src)
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	return base
}

func absURL(base, src string) string {
	if src == "" || strings.HasPrefix(src, "http") {
		return src
	}
	return base + "/" + strings.TrimPrefix(src, "/")
}

// cleanStory drops the "Read more"/"View more" toggle elements, then cleans the
// remaining text (including the hidden continuation span).
func cleanStory(s string) string {
	s = readmoreARe.ReplaceAllString(s, "")
	s = readmoreSpan.ReplaceAllString(s, "")
	return cleanText(s)
}

func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

func snippet(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

func (s *Scraper) send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) {
	select {
	case out <- r:
	case <-ctx.Done():
	}
}
