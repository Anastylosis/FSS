package visitx

import (
	"context"
	"encoding/json"
	"fmt"
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
	defaultBase = "https://www.visit-x.net"
	siteID      = "visitx"
	perPage     = 100
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?visit-x\.net/\w+/amateur/([^/]+)(?:/videos)?/?(?:\?.*)?$`)
	modelRe = regexp.MustCompile(`/amateur/([^/]+)`)
	tokenRe = regexp.MustCompile(`"vxqlAccessToken"\s*:\s*"([^"]+)"`)
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{
		"visit-x.net/{lang}/amateur/{model}/",
		"visit-x.net/{lang}/amateur/{model}/videos/",
	}
}
func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func modelFromURL(u string) string {
	m := modelRe.FindStringSubmatch(u)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	modelName := modelFromURL(studioURL)
	if modelName == "" {
		send(ctx, out, scraper.Error(fmt.Errorf("cannot extract model name from %s", studioURL)))
		return
	}

	token, err := s.fetchToken(ctx, studioURL)
	if err != nil {
		send(ctx, out, scraper.Error(fmt.Errorf("fetch token: %w", err)))
		return
	}

	for offset := 0; ; offset += perPage {
		if ctx.Err() != nil {
			return
		}

		if offset > 0 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "visitx: fetching page %d", offset)

		videos, total, err := s.fetchVideos(ctx, token, modelName, offset)
		if err != nil {
			send(ctx, out, scraper.Error(err))
			return
		}

		if len(videos) == 0 {
			return
		}

		if offset == 0 && total > 0 {
			scraper.Debugf(1, "visitx: %d total scenes", total)
			if !send(ctx, out, scraper.Progress(total)) {
				return
			}
		}

		now := time.Now().UTC()
		for _, v := range videos {
			id := strconv.Itoa(v.ID)

			if opts.KnownIDs[id] {
				scraper.Debugf(1, "visitx: hit known ID, stopping early")
				send(ctx, out, scraper.StoppedEarly())
				return
			}

			scene := toScene(v, studioURL, now)
			if !send(ctx, out, scraper.Scene(scene)) {
				return
			}
		}

		if offset+len(videos) >= total {
			return
		}
	}
}

func send(ctx context.Context, ch chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case ch <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scraper) fetchToken(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	m := tokenRe.FindSubmatch(body)
	if len(m) < 2 {
		return "", fmt.Errorf("vxqlAccessToken not found in page HTML")
	}
	return string(m[1]), nil
}

const videosQuery = `query($name:String!,$first:Int!,$offset:Int!){
  model(name:$name){
    id
    name
    videos_v2(first:$first,offset:$offset,order:newest){
      total
      items{
        id
        title(language:EN)
        description(language:EN)
        duration(format:sec)
        released
        free
        slug
        linkVX
        viewCount
        price{value currency}
        basePrice{value currency}
        preview{images(size:w640){url}}
        tagList{label}
        rating{likes dislikes}
        model{name}
      }
    }
  }
}`

func (s *Scraper) fetchVideos(ctx context.Context, token, modelName string, offset int) ([]gqlVideo, int, error) {
	reqBody := gqlRequest{
		Query: videosQuery,
		Variables: map[string]any{
			"name":   modelName,
			"first":  perPage,
			"offset": offset,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, err
	}

	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		Method: "POST",
		URL:    s.base + "/vxql",
		Body:   body,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentChrome)
			h["Content-Type"] = "application/json"
			h["Authorization"] = "Bearer " + token
			return h
		}(),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("graphql request offset %d: %w", offset, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var gqlResp gqlResponse
	if err := httpx.DecodeJSON(resp.Body, &gqlResp); err != nil {
		return nil, 0, fmt.Errorf("decode graphql response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, 0, fmt.Errorf("graphql error: %s", gqlResp.Errors[0].Message)
	}

	if gqlResp.Data.Model == nil {
		return nil, 0, fmt.Errorf("model %q not found", modelName)
	}

	vids := gqlResp.Data.Model.VideosV2
	return vids.Items, vids.Total, nil
}

// GraphQL types.

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type gqlResponse struct {
	Data struct {
		Model *gqlModel `json:"model"`
	} `json:"data"`
	Errors []gqlError `json:"errors"`
}

type gqlError struct {
	Message string `json:"message"`
}

type gqlModel struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	VideosV2 struct {
		Total int        `json:"total"`
		Items []gqlVideo `json:"items"`
	} `json:"videos_v2"`
}

type gqlVideo struct {
	ID          int            `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Duration    string         `json:"duration"`
	Released    string         `json:"released"`
	Free        bool           `json:"free"`
	Slug        string         `json:"slug"`
	LinkVX      string         `json:"linkVX"`
	ViewCount   int            `json:"viewCount"`
	Price       *gqlPrice      `json:"price"`
	BasePrice   *gqlPrice      `json:"basePrice"`
	Preview     *gqlPreview    `json:"preview"`
	TagList     []gqlTag       `json:"tagList"`
	Rating      *gqlRating     `json:"rating"`
	Model       *gqlVideoModel `json:"model"`
}

type gqlPrice struct {
	Value    float64 `json:"value"`
	Currency string  `json:"currency"`
}

type gqlPreview struct {
	Images []gqlImage `json:"images"`
}

type gqlImage struct {
	URL string `json:"url"`
}

type gqlVideoModel struct {
	Name string `json:"name"`
}

type gqlTag struct {
	Label string `json:"label"`
}

type gqlRating struct {
	Likes    int `json:"likes"`
	Dislikes int `json:"dislikes"`
}

func toScene(v gqlVideo, studioURL string, now time.Time) models.Scene {
	id := strconv.Itoa(v.ID)

	sc := models.Scene{
		ID:        id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     v.Title,
		URL:       v.LinkVX,
		Views:     v.ViewCount,
		ScrapedAt: now,
	}

	if dur, err := strconv.Atoi(v.Duration); err == nil {
		sc.Duration = dur
	}

	if v.Released != "" {
		if t, err := time.Parse(time.RFC3339, v.Released); err == nil {
			sc.Date = t.UTC()
		}
	}

	sc.Description = strings.TrimSpace(v.Description)

	if v.Model != nil && v.Model.Name != "" {
		sc.Studio = v.Model.Name
		sc.Performers = []string{v.Model.Name}
	}

	if v.Preview != nil && len(v.Preview.Images) > 0 {
		sc.Thumbnail = v.Preview.Images[0].URL
	}

	if v.TagList != nil {
		tags := make([]string, 0, len(v.TagList))
		for _, t := range v.TagList {
			if t.Label != "" {
				tags = append(tags, t.Label)
			}
		}
		sc.Tags = tags
	}

	if v.Rating != nil {
		sc.Likes = v.Rating.Likes
	}

	regular := 0.0
	if v.BasePrice != nil {
		regular = v.BasePrice.Value
	}
	effective := regular
	if v.Price != nil {
		effective = v.Price.Value
	}

	ps := models.PriceSnapshot{
		Date:    now,
		Regular: regular,
		IsFree:  v.Free,
	}
	if !v.Free && effective < regular && regular > 0 {
		ps.IsOnSale = true
		ps.Discounted = effective
		ps.DiscountPercent = int((1 - effective/regular) * 100)
	}
	sc.AddPrice(ps)

	return sc
}
