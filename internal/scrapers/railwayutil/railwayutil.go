package railwayutil

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

var APIBase = "https://sites-api-production.up.railway.app"

type SiteConfig struct {
	ID       string
	SiteCode string
	Studio   string
	SiteBase string
	Patterns []string
	MatchRe  *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string         { return s.cfg.ID }
func (s *Scraper) Patterns() []string { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool {
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type APIVideo struct {
	ID       string `json:"_id"`
	Name     string `json:"name"`
	Site     string `json:"site"`
	Group    int    `json:"group"`
	Video4K  bool   `json:"video4K"`
	Duration string `json:"duration"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: fetching all videos from API", s.cfg.ID)
	videos, err := s.fetchAll(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("fetch videos: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	filter := ParseFilter(studioURL)
	if filter != "" {
		scraper.Debugf(1, "%s: detected model filter: %s", s.cfg.ID, filter)
	}
	var filtered []APIVideo
	for _, v := range videos {
		if filter == "" || matchesFilter(v.Name, filter) {
			filtered = append(filtered, v)
		}
	}
	scraper.Debugf(1, "%s: %d total scenes", s.cfg.ID, len(filtered))

	select {
	case out <- scraper.Progress(len(filtered)):
	case <-ctx.Done():
		return
	}

	now := time.Now().UTC()
	for _, v := range filtered {
		if opts.KnownIDs[v.ID] {
			continue
		}
		scene := s.toScene(studioURL, v, now)
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) fetchAll(ctx context.Context) ([]APIVideo, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: APIBase + "/videos/" + s.cfg.SiteCode,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Accept"] = "application/json"
			return h
		}(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var videos []APIVideo
	if err := httpx.DecodeJSON(resp.Body, &videos); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return videos, nil
}

var modelPathRe = regexp.MustCompile(`(?i)#/models/(.+)`)

func ParseFilter(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return ""
	}
	fragment := u.Fragment
	if fragment == "" {
		if idx := strings.Index(studioURL, "#"); idx >= 0 {
			fragment = studioURL[idx+1:]
		}
	}
	if m := modelPathRe.FindStringSubmatch("#" + fragment); m != nil {
		name, _ := url.PathUnescape(m[1])
		return strings.ToLower(strings.TrimSpace(name))
	}
	return ""
}

func matchesFilter(videoName, filter string) bool {
	return strings.ToLower(ExtractPerformer(videoName)) == filter
}

var performerRe = regexp.MustCompile(`^(.+?)\s+(?:\d+|all)$`)

func ExtractPerformer(name string) string {
	if m := performerRe.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return name
}

// sceneURL builds a per-scene link. The tour is an Angular hash-router whose
// only content route is `/#/models/{name}` — there is no per-scene page — so
// every scene by one performer used to share a single URL and collapse into
// one entry downstream. The video id is carried as a query on that route,
// which the router ignores, so the link still opens the model's page while
// each scene keeps a distinct URL.
func (s *Scraper) sceneURL(performer, videoID string) string {
	u := s.cfg.SiteBase + "/#/models/" + url.PathEscape(performer)
	if videoID != "" {
		u += "?video=" + url.QueryEscape(videoID)
	}
	return u
}

func (s *Scraper) toScene(studioURL string, v APIVideo, now time.Time) models.Scene {
	performer := ExtractPerformer(v.Name)
	// The API publishes no image, so the thumbnail is derived from the site's
	// own naming convention ("{SiteCode} {Name}.jpg" under /assets/images).
	// It is a convention, not a published URL: a scene the site names
	// differently there simply has no image.
	thumbnail := s.cfg.SiteBase + "/assets/images/" + url.PathEscape(s.cfg.SiteCode+" "+v.Name) + ".jpg"

	scene := models.Scene{
		ID:         v.ID,
		SiteID:     s.cfg.ID,
		StudioURL:  studioURL,
		Title:      v.Name,
		URL:        s.sceneURL(performer, v.ID),
		Thumbnail:  thumbnail,
		Duration:   ParseDuration(v.Duration),
		Performers: []string{performer},
		Studio:     s.cfg.Studio,
		ScrapedAt:  now,
	}
	if v.Video4K {
		scene.Resolution = "4K"
	}
	return scene
}

func ParseDuration(s string) int {
	parts := strings.Split(s, ":")
	total := 0
	for _, p := range parts {
		n, _ := strconv.Atoi(p)
		total = total*60 + n
	}
	return total
}
