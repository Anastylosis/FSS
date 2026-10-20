// Package sarahgregoryspanking scrapes the static tour pages of
// sarahgregoryspanking.com. See docs/scrapers.md.
package sarahgregoryspanking

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

const (
	siteID      = "sarahgregoryspanking"
	studioName  = "Sarah Gregory Spanking"
	defaultBase = "https://www.sarahgregoryspanking.com"
)

var (
	matchRe   = regexp.MustCompile(`^https?://(?:www\.)?sarahgregoryspanking\.com(?:/|$)`)
	episodeRe = regexp.MustCompile(`(?s)<div class="episode">(.*?)<!--EPISODE FINISH HERE-->`)
	titleRe   = regexp.MustCompile(`(?s)<h1>(.*?)</h1>`)
	descRe    = regexp.MustCompile(`(?s)<p>(.*?)</p>`)
	posterRe  = regexp.MustCompile(`poster="([^"]+)"`)
	imgRe     = regexp.MustCompile(`<img src="(/new_updates/[^"]+)"`)
	tagRe     = regexp.MustCompile(`<[^>]+>`)
	suffixRe  = regexp.MustCompile(`-(?:bg|new\d+|\d+)$`)
	slugRe    = regexp.MustCompile(`[^a-z0-9]+`)
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: defaultBase}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"sarahgregoryspanking.com", "sarahgregoryspanking.com/tour.html"}
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
		pageURL := s.pageURL(page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := s.parsePage(body, studioURL, now)
		if len(scenes) == 0 {
			// Past the last page the site 302s to /join.html, which has no
			// episodes. On page 1 that would mean the markup changed.
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no episodes on tour page"))
			}
			return scraper.PageResult{Done: true}, nil
		}
		fresh := scenes[:0]
		for _, sc := range scenes {
			if !seen[sc.ID] {
				seen[sc.ID] = true
				fresh = append(fresh, sc)
			}
		}
		return scraper.PageResult{Scenes: fresh, Continue: true}, nil
	})
}

func (s *Scraper) pageURL(page int) string {
	if page == 1 {
		return s.base + "/tour.html"
	}
	return fmt.Sprintf("%s/tour%d.html", s.base, page)
}

func (s *Scraper) parsePage(body []byte, studioURL string, now time.Time) []models.Scene {
	var scenes []models.Scene
	for _, m := range episodeRe.FindAllSubmatch(body, -1) {
		block := string(m[1])
		tm := titleRe.FindStringSubmatch(block)
		if tm == nil {
			continue
		}
		title := cleanText(tm[1])
		if title == "" {
			continue
		}
		var desc string
		if dm := descRe.FindStringSubmatch(block); dm != nil {
			desc = cleanText(dm[1])
		}
		var thumb, firstImg string
		if pm := posterRe.FindStringSubmatch(block); pm != nil {
			thumb = s.base + pm[1]
		}
		if im := imgRe.FindStringSubmatch(block); im != nil {
			firstImg = im[1]
			if thumb == "" {
				thumb = s.base + firstImg
			}
		}
		id := sceneID(firstImg, title)
		scenes = append(scenes, models.Scene{
			ID:          id,
			SiteID:      siteID,
			StudioURL:   studioURL,
			Studio:      studioName,
			Title:       title,
			URL:         s.base + "/tour.html#" + id,
			Description: desc,
			Thumbnail:   thumb,
			ScrapedAt:   now,
		})
	}
	return scenes
}

// sceneID derives the id from the site's own image slug
// (/new_updates/{slug}-2.jpg, {slug}-new1.jpg), falling back to the title.
func sceneID(img, title string) string {
	if img != "" {
		base := strings.TrimSuffix(path.Base(img), path.Ext(img))
		if slug := suffixRe.ReplaceAllString(base, ""); slug != "" && slug != base {
			return slug
		}
	}
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(title), "-"), "-")
}

func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
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
