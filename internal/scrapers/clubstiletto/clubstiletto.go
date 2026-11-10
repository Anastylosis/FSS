// Package clubstiletto scrapes the video clips of Club Stiletto
// (clubstiletto.com), a Drupal "Globalkink" site whose catalogue listing
// mixes clips with galleries and stories. See docs/scrapers.md.
package clubstiletto

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID         = "clubstiletto"
	studioName     = "Club Stiletto"
	defaultWorkers = 4
)

var siteBase = "https://www.clubstiletto.com"

var (
	matchRe   = regexp.MustCompile(`^https?://(?:www\.)?clubstiletto\.com(?:/front-page-all)?/?(?:[?#].*)?$`)
	rowSplit  = regexp.MustCompile(`<div class="views-row views-row-\d+ `)
	ribbonRe  = regexp.MustCompile(`<div class="ribbon-([A-Z]\w*)">`)
	hrefRe    = regexp.MustCompile(`<a href="(/content/[^"]+)"`)
	nidRe     = regexp.MustCompile(`/cart/add/(\d+)`)
	thumbRe   = regexp.MustCompile(`image-main"><img [^>]*src="([^"]+)"`)
	titleRe   = regexp.MustCompile(`(?s)views-field-title".*?<div class="beautytips" title="[^"]*">(.*?)</div>`)
	dateRe    = regexp.MustCompile(`(?s)field-name-post-date.*?field-item even">([^<]+)<`)
	runtimeRe = regexp.MustCompile(`runtime:\s*(\d+)\s*min`)
	bodyRe    = regexp.MustCompile(`(?s)property="content:encoded">(.*?)</div></div></div>`)
	mistressR = regexp.MustCompile(`(?s)field-name-field-mistress.*?</div></div></div>`)
	anchorRe  = regexp.MustCompile(`(?s)<a [^>]*>(.*?)</a>`)
	brRe      = regexp.MustCompile(`(?i)</p>|<br\s*/?>`)
	stripRe   = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"clubstiletto.com", "clubstiletto.com/front-page-all"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type card struct {
	id, path, title, thumb string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "%s: walking /front-page-all (videos only), %d detail workers", siteID, workers)

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/front-page-all?page=%d", siteBase, page-1)
		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards, rows := parseCards(string(body))
		scenes := make([]models.Scene, len(cards))
		errs := make([]error, len(cards))

		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		for i, c := range cards {
			if opts.KnownIDs[c.id] {
				scenes[i] = models.Scene{ID: c.id}
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				if !scraper.Pace(ctx, opts.Delay) {
					errs[i] = ctx.Err()
					return
				}
				detail, err := s.get(ctx, siteBase+c.path)
				if err != nil {
					errs[i] = err
					return
				}
				scenes[i] = toScene(c, string(detail), studioURL, now)
			}()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return scraper.PageResult{}, err
			}
		}
		// A page of nothing but galleries and stories has no scenes yet is
		// not the end of the catalogue.
		return scraper.PageResult{Scenes: scenes, Continue: rows > 0, Done: rows == 0}, nil
	})
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// parseCards returns the listing's video rows and the number of rows of any
// kind, which tells an exhausted listing from a page of galleries.
func parseCards(page string) (cards []card, rows int) {
	parts := rowSplit.Split(page, -1)
	seen := map[string]bool{}
	for _, row := range parts[1:] {
		rows++
		if r := ribbonRe.FindStringSubmatch(row); r == nil || r[1] != "Video" {
			continue
		}
		href := hrefRe.FindStringSubmatch(row)
		nid := nidRe.FindStringSubmatch(row)
		if href == nil || nid == nil || seen[nid[1]] {
			continue
		}
		seen[nid[1]] = true
		c := card{id: nid[1], path: html.UnescapeString(href[1])}
		if t := titleRe.FindStringSubmatch(row); t != nil {
			c.title = clean(t[1])
		}
		if t := thumbRe.FindStringSubmatch(row); t != nil {
			c.thumb = html.UnescapeString(t[1])
		}
		cards = append(cards, c)
	}
	return cards, rows
}

func toScene(c card, page, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:        c.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     c.title,
		URL:       siteBase + c.path,
		Thumbnail: c.thumb,
		Studio:    studioName,
		ScrapedAt: now,
	}
	if m := dateRe.FindStringSubmatch(page); m != nil {
		if t, err := time.Parse("Monday, January 2, 2006 - 15:04", strings.TrimSpace(html.UnescapeString(m[1]))); err == nil {
			sc.Date = t.UTC()
		}
	}
	if m := runtimeRe.FindStringSubmatch(page); m != nil {
		n, _ := strconv.Atoi(m[1])
		sc.Duration = n * 60
	}
	if m := bodyRe.FindStringSubmatch(page); m != nil {
		sc.Description = strings.TrimSpace(clean(brRe.ReplaceAllString(m[1], "\n")))
	}
	if m := mistressR.FindString(page); m != "" {
		for _, a := range anchorRe.FindAllStringSubmatch(m, -1) {
			if name := clean(a[1]); name != "" {
				sc.Performers = append(sc.Performers, name)
			}
		}
	}
	return sc
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, " "))), " ")
}
