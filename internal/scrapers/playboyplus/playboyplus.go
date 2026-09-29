package playboyplus

import (
	"context"
	"encoding/json"
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
	siteID     = "playboyplus"
	siteBase   = "https://www.playboyplus.com"
	studioName = "Playboy Plus"

	algoliaAppID = "TSMKFA364Q"
	algoliaHost  = "https://TSMKFA364Q-dsn.algolia.net"
	indexName    = "all_photosets_latest_desc"
	imageCDN     = "https://transform.gammacdn.com/media"
	hitsPerPage  = 100
)

var (
	siteRe   = regexp.MustCompile(`^https?://(?:www\.)?playboyplus\.com(?:/|$)`)
	modelRe  = regexp.MustCompile(`/model/view/[^/]+/(\d+)`)
	apiKeyRe = regexp.MustCompile(`"algolia"\s*:\s*\{[^}]*"apiKey"\s*:\s*"([^"]+)"`)
)

type Scraper struct {
	Client      *http.Client
	algoliaHost string
	siteBaseURL string
}

func New() *Scraper {
	return &Scraper{
		Client:      httpx.NewClient(30 * time.Second),
		algoliaHost: algoliaHost,
		siteBaseURL: siteBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"playboyplus.com",
		"playboyplus.com/en/model/view/{name}/{id}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return siteRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	apiKey, err := s.fetchAPIKey(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	var facetFilter string
	if m := modelRe.FindStringSubmatch(studioURL); m != nil {
		name, err := s.resolveActorName(ctx, apiKey, m[1])
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("resolving actor name: %w", err)):
			case <-ctx.Done():
			}
			return
		}
		scraper.Debugf(1, "%s: scraping model page (%s)", siteID, name)
		facetFilter = "actors.name:" + name
	}

	for page := 0; ; page++ {
		if ctx.Err() != nil {
			return
		}

		if page > 0 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "%s: fetching page %d", siteID, page)

		hits, total, err := s.fetchPage(ctx, apiKey, page, facetFilter)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		if len(hits) == 0 {
			return
		}

		if page == 0 && total > 0 {
			scraper.Debugf(1, "%s: %d total photosets", siteID, total)
			select {
			case out <- scraper.Progress(total):
			case <-ctx.Done():
				return
			}
		}

		now := time.Now().UTC()
		for _, hit := range hits {
			id := strconv.Itoa(hit.SetID)
			if opts.KnownIDs[id] {
				scraper.Debugf(1, "%s: hit known ID, stopping early", siteID)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}

			scene := toScene(studioURL, hit, now)
			select {
			case out <- scraper.Scene(scene):
			case <-ctx.Done():
				return
			}
		}

		if (page+1)*hitsPerPage >= total {
			return
		}
	}
}

func (s *Scraper) fetchAPIKey(ctx context.Context) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     s.siteBaseURL + "/en/updates",
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", fmt.Errorf("fetching API key: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading page for API key: %w", err)
	}

	m := apiKeyRe.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("algolia API key not found in page source")
	}
	return string(m[1]), nil
}

func (s *Scraper) resolveActorName(ctx context.Context, apiKey, actorID string) (string, error) {
	q := algoliaQuery{
		Query:        "",
		HitsPerPage:  1,
		Page:         0,
		FacetFilters: [][]string{{"actor_id:" + actorID}},
	}
	body, err := json.Marshal(q)
	if err != nil {
		return "", err
	}

	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:  fmt.Sprintf("%s/1/indexes/all_actors_latest_desc/query", s.algoliaHost),
		Body: body,
		Headers: map[string]string{
			"x-algolia-application-id": algoliaAppID,
			"x-algolia-api-key":        apiKey,
			"Referer":                  s.siteBaseURL + "/",
			"Content-Type":             "application/json",
		},
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Hits []struct {
			Name string `json:"name"`
		} `json:"hits"`
	}
	if err := httpx.DecodeJSON(resp.Body, &result); err != nil {
		return "", err
	}
	if len(result.Hits) == 0 {
		return "", fmt.Errorf("actor %s not found", actorID)
	}
	return result.Hits[0].Name, nil
}

func (s *Scraper) fetchPage(ctx context.Context, apiKey string, page int, facetFilter string) ([]photosetHit, int, error) {
	q := algoliaQuery{
		Query:       "",
		HitsPerPage: hitsPerPage,
		Page:        page,
		Filters:     "upcoming:0",
	}
	if facetFilter != "" {
		q.FacetFilters = [][]string{{facetFilter}}
	}

	body, err := json.Marshal(q)
	if err != nil {
		return nil, 0, err
	}

	host := s.algoliaHost
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:  fmt.Sprintf("%s/1/indexes/%s/query", host, indexName),
		Body: body,
		Headers: map[string]string{
			"x-algolia-application-id": algoliaAppID,
			"x-algolia-api-key":        apiKey,
			"Referer":                  s.siteBaseURL + "/",
			"Content-Type":             "application/json",
		},
	})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result algoliaResponse
	if err := httpx.DecodeJSON(resp.Body, &result); err != nil {
		return nil, 0, fmt.Errorf("decoding algolia response: %w", err)
	}
	return result.Hits, result.NbHits, nil
}

func toScene(studioURL string, hit photosetHit, now time.Time) models.Scene {
	performers := make([]string, len(hit.Actors))
	for i, a := range hit.Actors {
		performers[i] = a.Name
	}

	tags := make([]string, 0, len(hit.Categories))
	for _, c := range hit.Categories {
		tags = append(tags, c.Name)
	}

	var directors []string
	for _, d := range hit.Directors {
		directors = append(directors, d.Name)
	}
	director := strings.Join(directors, ", ")

	desc := hit.Description
	desc = strings.ReplaceAll(desc, "</br>", "\n")
	desc = strings.ReplaceAll(desc, "<br>", "\n")
	desc = strings.ReplaceAll(desc, "<br/>", "\n")
	desc = strings.ReplaceAll(desc, "<br />", "\n")
	desc = html.UnescapeString(desc)
	desc = strings.TrimSpace(desc)

	sceneURL := fmt.Sprintf("%s/en/update/%s/%d", siteBase, hit.URLTitle, hit.SetID)
	thumbnail := bestThumbnail(hit.MulticontentData)

	return models.Scene{
		ID:          strconv.Itoa(hit.SetID),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       hit.Title,
		URL:         sceneURL,
		Date:        parseDate(hit.DateOnline),
		Description: desc,
		Thumbnail:   thumbnail,
		Performers:  performers,
		Director:    director,
		Studio:      studioName,
		Tags:        tags,
		Series:      hit.SerieName,
		Likes:       hit.RatingsUp,
		ScrapedAt:   now,
	}
}

func bestThumbnail(mc multicontentData) string {
	preferred := []string{"contentHero", "halfCard", "quarterCard"}
	for _, name := range preferred {
		for _, img := range mc.NSFW {
			if img.Name == name {
				return imageCDN + "/" + img.File
			}
		}
	}
	if len(mc.NSFW) > 0 {
		return imageCDN + "/" + mc.NSFW[0].File
	}
	for _, img := range mc.SFW {
		return imageCDN + "/" + img.File
	}
	return ""
}

func parseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// ---- Algolia API types ----

type algoliaQuery struct {
	Query        string     `json:"query"`
	HitsPerPage  int        `json:"hitsPerPage"`
	Page         int        `json:"page"`
	Filters      string     `json:"filters,omitempty"`
	FacetFilters [][]string `json:"facetFilters,omitempty"`
}

type algoliaResponse struct {
	Hits    []photosetHit `json:"hits"`
	NbHits  int           `json:"nbHits"`
	NbPages int           `json:"nbPages"`
}

type photosetHit struct {
	SetID            int              `json:"set_id"`
	Title            string           `json:"title"`
	Description      string           `json:"description"`
	DateOnline       string           `json:"date_online"`
	URLTitle         string           `json:"url_title"`
	NumOfPictures    string           `json:"num_of_pictures"`
	SiteName         string           `json:"sitename"`
	SiteNamePretty   string           `json:"sitename_pretty"`
	SerieName        string           `json:"serie_name"`
	Actors           []actor          `json:"actors"`
	Directors        []director       `json:"directors"`
	Categories       []category       `json:"categories"`
	MulticontentData multicontentData `json:"multicontent_data"`
	RatingsUp        int              `json:"ratings_up"`
	Views            int              `json:"views"`
	ObjectID         string           `json:"objectID"`
}

type actor struct {
	ActorID string `json:"actor_id"`
	Name    string `json:"name"`
	Gender  string `json:"gender"`
	URLName string `json:"url_name"`
}

type director struct {
	Name    string `json:"name"`
	URLName string `json:"url_name"`
}

type category struct {
	CategoryID string `json:"category_id"`
	Name       string `json:"name"`
	URLName    string `json:"url_name"`
}

type multicontentData struct {
	SFW  []multicontentImage `json:"sfw"`
	NSFW []multicontentImage `json:"nsfw"`
}

type multicontentImage struct {
	File     string `json:"file"`
	Name     string `json:"name"`
	Width    string `json:"width"`
	Height   string `json:"height"`
	MimeType string `json:"mime_type"`
}

func init() { scraper.Register(New()) }
