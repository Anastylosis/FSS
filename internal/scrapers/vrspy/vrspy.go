// Package vrspy scrapes vrspy.com. The tour is a Nuxt app, but everything it
// renders comes from a public GraphQL endpoint at api.vrspy.com/graphql, so
// this reads that directly rather than the page.
//
// Two queries do the work:
//
//   - `videos(page,size,sort:"publishedDate,desc")` — the catalogue, one page
//     at a time. It carries the id, slug, title, description, duration,
//     cover, preview, resolution, publish date and cast, but returns `tags`
//     and `price` as null.
//   - `videoByHrc(hrc)` — those two fields, per scene.
//
// A `/star/{hrc}` URL scrapes one performer's videos through
// `videosByActorHrc` instead.
package vrspy

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "vrspy"
	studioName = "VRSpy"
	siteBase   = "https://vrspy.com"
	pageSize   = 24
)

type Scraper struct {
	client *http.Client
	api    string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		api:    "https://api.vrspy.com/graphql",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"vrspy.com", "vrspy.com/star/{slug}"}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?vrspy\.com(?:/|$)`)
	starRe  = regexp.MustCompile(`/star/([a-z0-9-]+)`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- GraphQL ----

const listQuery = `query($page:Int!,$size:Int!){videos(page:$page,size:$size,sort:"publishedDate,desc"){` +
	`content{id name hrc description duration cover preview resolution free publishedDate actors{name}}` +
	`totalElements totalPages}}`

const actorQuery = `query($hrc:String!,$page:Int!,$size:Int!){videosByActorHrc(actorHrc:$hrc,page:$page,size:$size,sort:"publishedDate,desc"){` +
	`content{id name hrc description duration cover preview resolution free publishedDate actors{name}}` +
	`totalElements totalPages}}`

const detailQuery = `query($hrc:String!){videoByHrc(hrc:$hrc){price previousPrice free tags{name}}}`

type gqlVideo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Hrc           string   `json:"hrc"`
	Description   string   `json:"description"`
	Duration      int      `json:"duration"`
	Cover         string   `json:"cover"`
	Preview       string   `json:"preview"`
	Resolution    string   `json:"resolution"`
	Free          bool     `json:"free"`
	PublishedDate float64  `json:"publishedDate"`
	Price         *float64 `json:"price"`
	PreviousPrice *float64 `json:"previousPrice"`
	Actors        []struct {
		Name string `json:"name"`
	} `json:"actors"`
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags"`
}

type videoPage struct {
	Content       []gqlVideo `json:"content"`
	TotalElements int        `json:"totalElements"`
	TotalPages    int        `json:"totalPages"`
}

type gqlResponse struct {
	Data struct {
		Videos           *videoPage `json:"videos"`
		VideosByActorHrc *videoPage `json:"videosByActorHrc"`
		VideoByHrc       *gqlVideo  `json:"videoByHrc"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (s *Scraper) query(ctx context.Context, query string, vars map[string]any) (*gqlResponse, error) {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return nil, err
	}
	var resp gqlResponse
	err = httpx.DoJSON(ctx, s.client, httpx.Request{
		Method:  http.MethodPost,
		URL:     s.api,
		Body:    body,
		Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, siteBase),
	}, &resp)
	if err != nil {
		return nil, err
	}
	if len(resp.Errors) > 0 {
		return nil, scraper.ParseError(s.api, fmt.Errorf("graphql: %s", resp.Errors[0].Message))
	}
	return &resp, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	actor := ""
	if m := starRe.FindStringSubmatch(studioURL); m != nil {
		actor = m[1]
		scraper.Debugf(1, "%s: scraping performer %q", siteID, actor)
	} else {
		scraper.Debugf(1, "%s: scraping full catalogue", siteID)
	}

	items, ok := s.collect(ctx, actor, opts, out)
	if !ok || ctx.Err() != nil {
		return
	}
	s.fetchDetails(ctx, studioURL, items, opts, out)
}

func (s *Scraper) collect(ctx context.Context, actor string, opts scraper.ListOpts, out chan<- scraper.SceneResult) ([]gqlVideo, bool) {
	var items []gqlVideo
	sentTotal := false

	// The API pages from zero.
	for page := 0; ; page++ {
		if ctx.Err() != nil {
			return items, false
		}
		if page > 0 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return items, false
			}
		}
		scraper.Debugf(1, "%s: fetching page %d", siteID, page+1)

		var (
			resp *gqlResponse
			err  error
		)
		if actor != "" {
			resp, err = s.query(ctx, actorQuery, map[string]any{"hrc": actor, "page": page, "size": pageSize})
		} else {
			resp, err = s.query(ctx, listQuery, map[string]any{"page": page, "size": pageSize})
		}
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page+1, err)):
			case <-ctx.Done():
			}
			return items, false
		}

		vp := resp.Data.Videos
		if actor != "" {
			vp = resp.Data.VideosByActorHrc
		}
		if vp == nil || len(vp.Content) == 0 {
			return items, true
		}

		if !sentTotal && vp.TotalElements > 0 {
			sentTotal = true
			select {
			case out <- scraper.Progress(vp.TotalElements):
			case <-ctx.Done():
				return items, false
			}
		}

		for _, v := range vp.Content {
			if opts.KnownIDs[v.ID] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, v.ID)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return items, true
			}
			items = append(items, v)
		}

		if vp.TotalPages > 0 && page+1 >= vp.TotalPages {
			return items, true
		}
	}
}

func (s *Scraper) fetchDetails(ctx context.Context, studioURL string, items []gqlVideo, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(items) == 0 {
		return
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)

	work := make(chan gqlVideo, workers)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer close(work)

	now := time.Now().UTC()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for v := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				// The listing projection returns tags and price as null; only
				// the per-scene query fills them in. Losing that costs two
				// fields, not the scene.
				resp, err := s.query(ctx, detailQuery, map[string]any{"hrc": v.Hrc})
				if err != nil {
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", v.Hrc, err)):
					case <-ctx.Done():
						return
					}
				} else if d := resp.Data.VideoByHrc; d != nil {
					v.Tags = d.Tags
					v.Price = d.Price
					v.PreviousPrice = d.PreviousPrice
					v.Free = d.Free
				}
				select {
				case out <- scraper.Scene(toScene(v, studioURL, now)):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, v := range items {
		select {
		case work <- v:
		case <-ctx.Done():
			return
		}
	}
}

// ---- scene building ----

func toScene(v gqlVideo, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          v.ID,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       strings.TrimSpace(html.UnescapeString(v.Name)),
		URL:         siteBase + "/video/" + v.Hrc,
		Description: cleanDescription(v.Description),
		Thumbnail:   v.Cover,
		Preview:     v.Preview,
		Duration:    v.Duration,
		Resolution:  resolution(v.Resolution),
		Studio:      studioName,
		ScrapedAt:   now,
	}
	if v.PublishedDate > 0 {
		sc.Date = time.Unix(int64(v.PublishedDate), 0).UTC()
	}
	for _, a := range v.Actors {
		if n := strings.TrimSpace(a.Name); n != "" {
			sc.Performers = append(sc.Performers, n)
		}
	}
	for _, t := range v.Tags {
		if n := strings.TrimSpace(t.Name); n != "" {
			sc.Tags = append(sc.Tags, n)
		}
	}
	if snap, ok := priceSnapshot(v, now); ok {
		sc.AddPrice(snap)
	}
	return sc
}

// priceSnapshot builds a snapshot from the scene's price fields. A scene with
// neither a price nor a free flag records nothing: the API returns null for
// "not sold separately", which is not the same as costing zero.
func priceSnapshot(v gqlVideo, now time.Time) (models.PriceSnapshot, bool) {
	if v.Free {
		return models.PriceSnapshot{Date: now, IsFree: true}, true
	}
	if v.Price == nil {
		return models.PriceSnapshot{}, false
	}
	snap := models.PriceSnapshot{Date: now, Regular: *v.Price}
	if v.PreviousPrice != nil && *v.PreviousPrice > *v.Price {
		snap.Regular = *v.PreviousPrice
		snap.Discounted = *v.Price
		snap.IsOnSale = true
		snap.DiscountPercent = int((1 - *v.Price / *v.PreviousPrice) * 100)
	}
	return snap, true
}

// resolution turns the API's "P8K" enum into the plain height label the rest of
// FSS stores. Anything unrecognised is passed through unchanged.
func resolution(raw string) string {
	if raw == "" {
		return ""
	}
	return strings.TrimPrefix(raw, "P")
}

var (
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	blankRe = regexp.MustCompile(`\n{3,}`)
)

// cleanDescription strips the HTML the API returns (headings, paragraphs and
// promo links into the site's own tag pages) down to plain text.
func cleanDescription(raw string) string {
	if raw == "" {
		return ""
	}
	s := strings.ReplaceAll(raw, "</p>", "\n")
	s = strings.ReplaceAll(s, "</h4>", "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	s = strings.Join(lines, "\n")
	s = blankRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
