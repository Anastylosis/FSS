package stashbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/config"
	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "stashbox" }

func (s *Scraper) Patterns() []string {
	insts, _ := getInstances()
	var patterns []string
	for _, inst := range insts {
		base := inst.baseURL
		patterns = append(patterns,
			base+"/performers/{id}",
			base+"/studios/{id}",
		)
	}
	if len(patterns) == 0 {
		patterns = append(patterns, "(configure stashbox in config.yaml)")
	}
	return patterns
}

var pathRe = regexp.MustCompile(`^/(performers|studios)/([0-9a-f-]{36})$`)

func (s *Scraper) MatchesURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if !pathRe.MatchString(parsed.Path) {
		return false
	}
	insts, _ := getInstances()
	for _, inst := range insts {
		if strings.EqualFold(parsed.Host, inst.host) {
			return true
		}
	}
	return false
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	parsed, err := url.Parse(studioURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	m := pathRe.FindStringSubmatch(parsed.Path)
	if m == nil {
		return nil, fmt.Errorf("URL must match /{performers|studios}/{uuid}: %s", studioURL)
	}

	insts, err := getInstances()
	if err != nil {
		return nil, err
	}
	var inst *instance
	for i := range insts {
		if strings.EqualFold(parsed.Host, insts[i].host) {
			inst = &insts[i]
			break
		}
	}
	if inst == nil {
		return nil, fmt.Errorf("no stashbox config found for host %s, add it to config.yaml under stashbox", parsed.Host)
	}

	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, *inst, m[1], m[2], opts, out)
	return out, nil
}

// ---- instance config ----

type instance struct {
	graphqlURL string
	apiKey     string
	host       string
	baseURL    string
	siteID     string
}

var (
	instances     []instance
	instancesErr  error
	instancesOnce sync.Once
)

func getInstances() ([]instance, error) {
	instancesOnce.Do(func() {
		cfg, err := config.Load()
		if err != nil {
			instancesErr = fmt.Errorf("loading stashbox config: %w", err)
			return
		}
		if len(cfg.Stashbox) == 0 {
			return
		}
		for _, sb := range cfg.Stashbox {
			u, err := url.Parse(sb.URL)
			if err != nil {
				continue
			}
			host := u.Host
			baseURL := u.Scheme + "://" + host
			siteID := deriveSiteID(host)
			instances = append(instances, instance{
				graphqlURL: sb.URL,
				apiKey:     sb.APIKey,
				host:       host,
				baseURL:    baseURL,
				siteID:     siteID,
			})
		}
	})
	return instances, instancesErr
}

func deriveSiteID(host string) string {
	h := strings.TrimPrefix(host, "www.")
	if i := strings.IndexByte(h, ':'); i > 0 {
		h = h[:i]
	}
	if i := strings.IndexByte(h, '.'); i > 0 {
		return h[:i]
	}
	return h
}

// ---- GraphQL types ----

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type gqlResponse struct {
	Data struct {
		QueryScenes struct {
			Count  int        `json:"count"`
			Scenes []gqlScene `json:"scenes"`
		} `json:"queryScenes"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type gqlScene struct {
	ID          string          `json:"id"`
	Title       *string         `json:"title"`
	Details     *string         `json:"details"`
	ReleaseDate *string         `json:"release_date"`
	Duration    *int            `json:"duration"`
	Director    *string         `json:"director"`
	Code        *string         `json:"code"`
	Studio      *gqlStudio      `json:"studio"`
	Tags        []gqlTag        `json:"tags"`
	Performers  []gqlAppearance `json:"performers"`
	Images      []gqlImage      `json:"images"`
	URLs        []gqlURL        `json:"urls"`
}

type gqlStudio struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type gqlTag struct {
	Name string `json:"name"`
}

type gqlAppearance struct {
	Performer gqlPerformer `json:"performer"`
	As        *string      `json:"as"`
}

type gqlPerformer struct {
	Name string `json:"name"`
}

type gqlImage struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type gqlURL struct {
	URL  string  `json:"url"`
	Site gqlSite `json:"site"`
}

type gqlSite struct {
	Name string `json:"name"`
}

const scenesQuery = `query($input: SceneQueryInput!) {
  queryScenes(input: $input) {
    count
    scenes {
      id title details release_date duration director code
      studio { id name }
      tags { name }
      performers { performer { name } as }
      images { url width height }
      urls { url site { name } }
    }
  }
}`

const perPage = 100

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, inst instance, entityType, entityID string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Paginate(ctx, opts, "stashbox", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		input := buildInput(entityType, entityID, page)
		resp, err := s.queryScenes(ctx, inst, input)
		if err != nil {
			return scraper.PageResult{}, err
		}

		gqlScenes := resp.Data.QueryScenes.Scenes
		if len(gqlScenes) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			total = resp.Data.QueryScenes.Count
			if total <= 0 {
				total = len(gqlScenes)
			}
		}

		scenes := make([]models.Scene, len(gqlScenes))
		for i, gs := range gqlScenes {
			scenes[i] = toScene(studioURL, inst, gs)
		}
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   page*perPage >= resp.Data.QueryScenes.Count,
		}, nil
	})
}

func buildInput(entityType, entityID string, page int) map[string]any {
	input := map[string]any{
		"page":      page,
		"per_page":  perPage,
		"sort":      "DATE",
		"direction": "DESC",
	}
	switch entityType {
	case "performers":
		input["performers"] = map[string]any{
			"value":    []string{entityID},
			"modifier": "INCLUDES",
		}
	case "studios":
		input["parentStudio"] = entityID
	}
	return input
}

func toScene(studioURL string, inst instance, gs gqlScene) models.Scene {
	scene := models.Scene{
		ID:        gs.ID,
		SiteID:    inst.siteID,
		StudioURL: studioURL,
		URL:       inst.baseURL + "/scenes/" + gs.ID,
		ScrapedAt: time.Now().UTC(),
		// The scene ID is this stashbox's UUID, which is what makes it
		// matchable against scenes scraped from the sites themselves.
		ExternalIDs: map[string]string{inst.siteID: gs.ID},
	}

	if gs.Title != nil {
		scene.Title = *gs.Title
	}
	if gs.Details != nil {
		scene.Description = *gs.Details
	}
	if gs.ReleaseDate != nil && *gs.ReleaseDate != "" {
		if t, err := time.Parse("2006-01-02", *gs.ReleaseDate); err == nil {
			scene.Date = t
		}
	}
	if gs.Duration != nil && *gs.Duration > 0 {
		scene.Duration = *gs.Duration
	}
	if gs.Director != nil {
		scene.Director = *gs.Director
	}
	if gs.Studio != nil {
		scene.Studio = gs.Studio.Name
	}

	for _, p := range gs.Performers {
		name := p.Performer.Name
		if p.As != nil && *p.As != "" {
			name = *p.As
		}
		if name != "" {
			scene.Performers = append(scene.Performers, name)
		}
	}

	for _, t := range gs.Tags {
		if t.Name != "" {
			scene.Tags = append(scene.Tags, t.Name)
		}
	}

	if len(gs.Images) > 0 {
		best := gs.Images[0]
		for _, img := range gs.Images[1:] {
			if img.Width > best.Width {
				best = img
			}
		}
		scene.Thumbnail = best.URL
	}

	return scene
}

// ---- HTTP ----

func (s *Scraper) queryScenes(ctx context.Context, inst instance, input map[string]any) (gqlResponse, error) {
	body, err := json.Marshal(gqlRequest{
		Query:     scenesQuery,
		Variables: map[string]any{"input": input},
	})
	if err != nil {
		return gqlResponse{}, err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"ApiKey":       inst.apiKey,
	}

	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		Method:     http.MethodPost,
		URL:        inst.graphqlURL,
		Body:       body,
		Idempotent: true,
		Headers:    headers,
	})
	if err != nil {
		return gqlResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	var gqlResp gqlResponse
	if err := httpx.DecodeJSON(resp.Body, &gqlResp); err != nil {
		return gqlResponse{}, fmt.Errorf("decoding response: %w", err)
	}
	if len(gqlResp.Errors) > 0 {
		msgs := make([]string, len(gqlResp.Errors))
		for i, e := range gqlResp.Errors {
			msg := e.Message
			if inst.apiKey != "" {
				msg = strings.ReplaceAll(msg, inst.apiKey, "[redacted]")
			}
			msgs[i] = msg
		}
		return gqlResponse{}, fmt.Errorf("graphql error: %s", strings.Join(msgs, "; "))
	}
	return gqlResp, nil
}
