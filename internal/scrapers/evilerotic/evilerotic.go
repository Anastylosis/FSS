// Package evilerotic scrapes Evil Erotic (evilerotic.com). The listing cards
// carry every field the site publishes. See docs/scrapers.md.
package evilerotic

import (
	"context"
	"fmt"
	"html"
	"net/http"
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
	siteID     = "evilerotic"
	studioName = "Evil Erotic"
)

var siteBase = "https://evilerotic.com"

var (
	matchRe   = regexp.MustCompile(`^https?://(?:www\.)?evilerotic\.com(?:/|$)`)
	idRe      = regexp.MustCompile(`^[^>]*data-id="(\d+)"`)
	slugRe    = regexp.MustCompile(`class="block relative[^"]*card-thumb-link"`)
	hrefRe    = regexp.MustCompile(`<a href="/videos/([^"]+)"`)
	thumbRe   = regexp.MustCompile(`<img src="([^"]+)" alt="([^"]*)" class="video-thumb`)
	titleRe   = regexp.MustCompile(`class="card-title-link">([^<]*)</a>`)
	modelRe   = regexp.MustCompile(`class="vmeta-name">([^<]*)</span>`)
	dateRe    = regexp.MustCompile(`<time class="vmeta-date" datetime="([^"]+)"`)
	metaRe    = regexp.MustCompile(`class="card-info-meta">[^<]*?([\d:,]+)\s*</span>`)
	cardSplit = regexp.MustCompile(`<div class="group video-card`)
	svgRe     = regexp.MustCompile(`(?s)<svg.*?</svg>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string { return []string{"evilerotic.com", "evilerotic.com/videos"} }

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/videos?page=%d", siteBase, page)
		resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
		if err != nil {
			return scraper.PageResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := httpx.ReadBody(resp.Body)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parsePage(string(b), studioURL, now)
		if len(scenes) == 0 && page == 1 {
			return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("no video cards"))
		}
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func parsePage(body, studioURL string, now time.Time) []models.Scene {
	var scenes []models.Scene
	for _, card := range cardSplit.Split(body, -1)[1:] {
		card = svgRe.ReplaceAllString(card, "")
		id := idRe.FindStringSubmatch(card)
		href := hrefRe.FindStringSubmatch(card)
		if id == nil || href == nil || !slugRe.MatchString(card) {
			continue
		}
		sc := models.Scene{
			ID:        id[1],
			SiteID:    siteID,
			StudioURL: studioURL,
			URL:       siteBase + "/videos/" + href[1],
			Studio:    studioName,
			ScrapedAt: now,
		}
		if m := titleRe.FindStringSubmatch(card); m != nil {
			sc.Title = html.UnescapeString(strings.TrimSpace(m[1]))
		}
		if m := thumbRe.FindStringSubmatch(card); m != nil {
			sc.Thumbnail = html.UnescapeString(m[1])
			if sc.Title == "" {
				sc.Title = html.UnescapeString(m[2])
			}
		}
		for _, m := range modelRe.FindAllStringSubmatch(card, -1) {
			sc.Performers = append(sc.Performers, html.UnescapeString(strings.TrimSpace(m[1])))
		}
		if m := dateRe.FindStringSubmatch(card); m != nil {
			if t, err := time.Parse("2006-01-02", m[1]); err == nil {
				sc.Date = t.UTC()
			}
		}
		for _, m := range metaRe.FindAllStringSubmatch(card, -1) {
			if strings.Contains(m[1], ":") {
				sc.Duration = parseutil.ParseDurationColon(m[1])
			} else if n, err := strconv.Atoi(strings.ReplaceAll(m[1], ",", "")); err == nil {
				sc.Views = n
			}
		}
		if sc.Title != "" {
			scenes = append(scenes, sc)
		}
	}
	return scenes
}
