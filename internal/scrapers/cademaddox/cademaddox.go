// Package cademaddox scrapes Cade Maddox (cademaddox.com), an Elevated X tour
// whose listing cards carry the whole record. See docs/scrapers.md.
package cademaddox

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "cademaddox"
	studioName = "Cade Maddox"
	siteBase   = "https://cademaddox.com"
	maxPages   = 100
)

var (
	matchRe     = regexp.MustCompile(`^https?://(?:www\.)?cademaddox\.com(?:/|$)`)
	cardStartRe = regexp.MustCompile(`<div[^>]*class="updateDetails"`)
	urlRe       = regexp.MustCompile(`<a\s+href="([^"]*/updates/([^"/]+)\.html)"`)
	titleRe     = regexp.MustCompile(`(?s)<h4>\s*(.*?)\s*</h4>`)
	// Both the date and the runtime are rendered in an `availdate` span.
	availRe    = regexp.MustCompile(`class="availdate"[^>]*>\s*([^<]{3,40}?)\s*</span>`)
	runtimeRe  = regexp.MustCompile(`^(\d{1,2}:\d{2}(?::\d{2})?)\s*min`)
	thumbRes   = []*regexp.Regexp{regexp.MustCompile(`src0_1x="([^"]+)"`), regexp.MustCompile(`<img[^>]+src="([^"]+)"`)}
	setIDRe    = regexp.MustCompile(`id="set-target-(\d+)"`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"cademaddox.com", "cademaddox.com/categories/videos.html"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := map[string]bool{}
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if page > maxPages {
			return scraper.PageResult{Done: true}, nil
		}
		body, err := s.fetchPage(ctx, fmt.Sprintf("%s/categories/videos_%d_d.html", s.base, page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parseScenes(body, studioURL, now)
		fresh := scenes[:0]
		for _, sc := range scenes {
			if seen[sc.ID] {
				continue
			}
			seen[sc.ID] = true
			fresh = append(fresh, sc)
		}
		return scraper.PageResult{Scenes: fresh, Done: len(fresh) == 0}, nil
	})
}

// parseScenes reads the listing cards; the detail pages add nothing the card
// does not already carry.
func parseScenes(body, studioURL string, now time.Time) []models.Scene {
	starts := cardStartRe.FindAllStringIndex(body, -1)
	scenes := make([]models.Scene, 0, len(starts))
	for i, loc := range starts {
		end := len(body)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		card := body[loc[0]:end]

		m := urlRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		scene := models.Scene{
			ID:        m[2],
			SiteID:    siteID,
			StudioURL: studioURL,
			Studio:    studioName,
			URL:       html.UnescapeString(m[1]),
			ScrapedAt: now,
		}
		if id := setIDRe.FindStringSubmatch(card); id != nil {
			scene.ID = id[1]
		}
		if t := titleRe.FindStringSubmatch(card); t != nil {
			scene.Title = cleanText(t[1])
		}
		if scene.Title == "" {
			scene.Title = strings.ReplaceAll(m[2], "-", " ")
		}
		for _, re := range thumbRes {
			if th := re.FindStringSubmatch(card); th != nil {
				scene.Thumbnail = th[1]
				break
			}
		}
		for _, a := range availRe.FindAllStringSubmatch(card, -1) {
			value := cleanText(a[1])
			if rt := runtimeRe.FindStringSubmatch(value); rt != nil {
				scene.Duration = parseutil.ParseDurationColon(rt[1])
				continue
			}
			if d, err := parseutil.TryParseDate(value, "Jan 2, 2006", "January 2, 2006", "01/02/2006"); err == nil {
				scene.Date = d.UTC()
			}
		}
		scenes = append(scenes, scene)
	}
	return scenes
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
