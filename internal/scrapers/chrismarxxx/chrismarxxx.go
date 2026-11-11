// Package chrismarxxx scrapes Chris Marxxx's Marxxx Models site
// (marxxxmodels.com; chrismarxxx.com redirects there), a WordPress install
// whose video category (id 3) holds one post per clip with only a title, date
// and featured image. The performer is parsed from the title. See
// docs/scrapers.md.
package chrismarxxx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "chrismarxxx"
	studioName = "Chris Marxxx"
	perPage    = 100
	// videosCategory is "Most Recent Videos"; "Most Recent Photo Sets" and the
	// per-model pages live in other categories.
	videosCategory = 3
)

var siteBase = "https://www.marxxxmodels.com"

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:marxxxmodels|chrismarxxx)\.com(?:/|$)`)
	// nameStop is the first word of the clip description that follows the
	// performer's name in a title ("Gigi Sweets Is Horny", "Dharma Jones Solo #2").
	nameStop = map[string]bool{
		"solo": true, "wants": true, "is": true, "can't": true, "ass": true, "handjob": true, "the": true,
		"bj": true, "69": true, "slut": true, "bonus": true, "loves": true, "needs": true, "quickie": true,
		"anal": true, "feeling": true, "puppy": true,
	}
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"marxxxmodels.com", "chrismarxxx.com"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type post struct {
	ID    int    `json:"id"`
	Date  string `json:"date"`
	Link  string `json:"link"`
	Title struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Embedded struct {
		Media []struct {
			SourceURL string `json:"source_url"`
		} `json:"wp:featuredmedia"`
	} `json:"_embedded"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/wp-json/wp/v2/posts?categories=%d&per_page=%d&page=%d&orderby=date&order=desc&_embed=wp:featuredmedia&_fields=id,date,link,title,_embedded,_links",
			siteBase, videosCategory, perPage, page)
		var posts []post
		total, pages, err := s.getJSON(ctx, u, &posts)
		if err != nil {
			var se *httpx.StatusError
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusBadRequest {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, 0, len(posts))
		for _, p := range posts {
			scenes = append(scenes, toScene(p, studioURL, now))
		}
		res := scraper.PageResult{Scenes: scenes, Done: len(posts) < perPage || (pages > 0 && page >= pages)}
		if page == 1 {
			res.Total = total
		}
		return res, nil
	})
}

func (s *Scraper) getJSON(ctx context.Context, u string, v any) (total, pages int, err error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, siteBase)})
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	total, _ = strconv.Atoi(resp.Header.Get("X-WP-Total"))
	pages, _ = strconv.Atoi(resp.Header.Get("X-WP-TotalPages"))
	if err := httpx.DecodeJSON(resp.Body, v); err != nil {
		var se *json.SyntaxError
		if errors.As(err, &se) {
			return 0, 0, scraper.ParseError(u, err)
		}
		return 0, 0, err
	}
	return total, pages, nil
}

func toScene(p post, studioURL string, now time.Time) models.Scene {
	title := clean(p.Title.Rendered)
	sc := models.Scene{
		ID:        strconv.Itoa(p.ID),
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     title,
		URL:       p.Link,
		Studio:    studioName,
		ScrapedAt: now,
	}
	if t, err := time.Parse("2006-01-02T15:04:05", p.Date); err == nil {
		sc.Date = t.UTC()
	}
	if len(p.Embedded.Media) > 0 {
		sc.Thumbnail = p.Embedded.Media[0].SourceURL
	}
	if name := performer(title); name != "" {
		sc.Performers = []string{name}
	}
	return sc
}

// performer returns the leading name of a title, or "" when the first word is
// already a stop word or the name would run past four words.
func performer(title string) string {
	title = strings.TrimSpace(strings.TrimPrefix(title, "Bonus Video:"))
	var name []string
	for _, w := range strings.Fields(title) {
		if nameStop[strings.ToLower(strings.ReplaceAll(w, "’", "'"))] {
			break
		}
		name = append(name, w)
	}
	if len(name) == 0 || len(name) > 4 || len(name) == len(strings.Fields(title)) {
		return ""
	}
	return strings.Join(name, " ")
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}
