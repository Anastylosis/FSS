package watch4beauty

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
	siteID   = "watch4beauty"
	siteBase = "https://www.watch4beauty.com"
	apiBase  = siteBase + "/api"
	pageSize = 50
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?watch4beauty\.com(?:/|$)`)
	modelRe = regexp.MustCompile(`/model/([\w-]+)`)
)

type Scraper struct {
	Client *http.Client
	// apiBase is the API root used for *fetches*, a field so offline tests can
	// redirect them. The scene-URL and cover-URL builders keep the siteBase
	// const — those are public addresses, not things a test should rewrite.
	apiBase string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second), apiBase: apiBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"watch4beauty.com/",
		"watch4beauty.com/model/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if m := modelRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping model page %s", siteID, m[1])
		s.runModel(ctx, m[1], studioURL, opts, out)
		return
	}

	scraper.Debugf(1, "%s: scraping main listing", siteID)
	s.runListing(ctx, studioURL, opts, out)
}

func (s *Scraper) runListing(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	s.runListingFrom(ctx, studioURL, opts, out, s.apiBase)
}

func (s *Scraper) runListingFrom(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, base string) {
	now := time.Now().UTC()
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	var before string

	for {
		if ctx.Err() != nil {
			return
		}

		u := base + "/issues"
		if before != "" {
			u += "?before=" + url.QueryEscape(before)
		}
		issues, err := s.fetchIssuesFrom(ctx, u)
		if err != nil {
			select {
			case out <- scraper.Error(err):
			case <-ctx.Done():
			}
			return
		}
		if len(issues) == 0 {
			return
		}
		// The API mixes photo-only issues into the same feed; only an issue
		// with a video is a scene. Paging still counts the raw page, since the
		// cursor walks the unfiltered feed.
		page := issues
		issues = videoIssues(issues)

		type result struct {
			scene models.Scene
			order int
		}

		results := make([]result, len(issues))
		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)

		for i, issue := range issues {
			results[i] = result{scene: toScene(studioURL, issue, now), order: i}

			wg.Add(1)
			go func(idx int, slug string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				if ctx.Err() != nil {
					return
				}

				performers, err := s.fetchIssueModelsFrom(ctx, base+"/issues/"+slug+"/models")
				if err != nil {
					scraper.Debugf(1, "%s: failed to fetch models for %s: %v", siteID, slug, err)
					return
				}
				results[idx].scene.Performers = performers
			}(i, issue.SimpleTitle)
		}

		wg.Wait()

		for _, r := range results {
			if opts.KnownIDs != nil && opts.KnownIDs[r.scene.ID] {
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case out <- scraper.Scene(r.scene):
			case <-ctx.Done():
				return
			}
		}

		if len(page) < pageSize {
			return
		}

		before = page[len(page)-1].Datetime

		if !scraper.Pace(ctx, opts.Delay) {
			return
		}
	}
}

func (s *Scraper) runModel(ctx context.Context, modelSlug, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	s.runModelFrom(ctx, modelSlug, studioURL, opts, out, s.apiBase)
}

func (s *Scraper) runModelFrom(ctx context.Context, modelSlug, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, base string) {
	now := time.Now().UTC()

	updates, err := s.fetchModelUpdatesFrom(ctx, base+"/models/"+modelSlug+"/updates")
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	if len(updates) == 0 {
		return
	}

	modelName := updates[0].ModelNickname
	issues := videoIssues(updates[0].Issues)
	scraper.Debugf(1, "%s: model %s has %d issues", siteID, modelName, len(issues))

	if len(issues) > 0 {
		select {
		case out <- scraper.Progress(len(issues)):
		case <-ctx.Done():
			return
		}
	}

	for _, issue := range issues {
		if opts.KnownIDs != nil && opts.KnownIDs[strconv.Itoa(issue.ID)] {
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}

		scene := toScene(studioURL, issue, now)
		scene.Performers = []string{modelName}

		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}
}

// ---- API types ----

type issue struct {
	ID            int               `json:"issue_id"`
	Category      int               `json:"issue_category"`
	Datetime      string            `json:"issue_datetime"`
	Title         string            `json:"issue_title"`
	SimpleTitle   string            `json:"issue_simple_title"`
	Size          int               `json:"issue_size"`
	Text          string            `json:"issue_text"`
	Rating        float64           `json:"issue_rating"`
	VideoPresent  int               `json:"issue_video_present"`
	Tags          string            `json:"issue_tags"`
	Prefix        string            `json:"prefix"`
	CDNHost       string            `json:"cdn_host"`
	CoverMigrated int               `json:"cover_migrated"`
	Widecover     bool              `json:"widecover"`
	CoverFiles    map[string]string `json:"cover_files"`
}

// videoIssues drops the photo-only issues the feed mixes in. `issue_video_present`
// is the API's own flag; roughly half of a listing page is photo sets.
func videoIssues(in []issue) []issue {
	out := in[:0:0]
	for _, i := range in {
		if i.VideoPresent != 0 {
			out = append(out, i)
		}
	}
	return out
}

type issueModelsResp struct {
	IssueID int          `json:"issue_id"`
	Models  []issueModel `json:"Models"`
}

type issueModel struct {
	ModelID       int    `json:"model_id"`
	ModelNickname string `json:"model_nickname"`
	SimpleNick    string `json:"model_simple_nickname"`
}

type modelUpdatesResp struct {
	ModelID       int     `json:"model_id"`
	ModelNickname string  `json:"model_nickname"`
	Issues        []issue `json:"Issues"`
}

// ---- API calls ----

func (s *Scraper) fetchIssuesFrom(ctx context.Context, u string) ([]issue, error) {
	scraper.Debugf(1, "%s: fetching issues page %s", siteID, u)

	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: defaultHeaders(),
	})
	if err != nil {
		return nil, fmt.Errorf("fetching issues: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var issues []issue
	if err := httpx.DecodeJSON(resp.Body, &issues); err != nil {
		return nil, fmt.Errorf("decoding issues: %w", err)
	}
	return issues, nil
}

func (s *Scraper) fetchIssueModelsFrom(ctx context.Context, u string) ([]string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: defaultHeaders(),
	})
	if err != nil {
		return nil, fmt.Errorf("fetching issue models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result []issueModelsResp
	if err := httpx.DecodeJSON(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decoding issue models: %w", err)
	}

	if len(result) == 0 || len(result[0].Models) == 0 {
		return nil, nil
	}

	names := make([]string, len(result[0].Models))
	for i, m := range result[0].Models {
		names[i] = m.ModelNickname
	}
	return names, nil
}

func (s *Scraper) fetchModelUpdatesFrom(ctx context.Context, u string) ([]modelUpdatesResp, error) {
	scraper.Debugf(1, "%s: fetching model updates %s", siteID, u)

	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: defaultHeaders(),
	})
	if err != nil {
		return nil, fmt.Errorf("fetching model updates: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result []modelUpdatesResp
	if err := httpx.DecodeJSON(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decoding model updates: %w", err)
	}
	return result, nil
}

func defaultHeaders() map[string]string {
	return httpx.BrowserHeaders(httpx.UserAgentFirefox)
}

// ---- scene conversion ----

func toScene(studioURL string, iss issue, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        strconv.Itoa(iss.ID),
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     iss.Title,
		URL:       siteBase + "/updates/" + iss.SimpleTitle,
		Studio:    "Watch4Beauty",
		ScrapedAt: now,
	}

	if t, err := time.Parse(time.RFC3339, iss.Datetime); err == nil {
		scene.Date = t.UTC()
	} else if t, err := time.Parse("2006-01-02T15:04:05.000Z", iss.Datetime); err == nil {
		scene.Date = t.UTC()
	}

	scene.Description = iss.Text
	scene.Thumbnail = coverURL(iss)

	if iss.Tags != "" {
		for _, t := range strings.Split(iss.Tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				scene.Tags = append(scene.Tags, t)
			}
		}
	}

	if iss.Rating > 0 {
		scene.Likes = int(iss.Rating * 100)
	}

	return scene
}

func coverURL(iss issue) string {
	coverFile := ""
	for _, key := range []string{"wide-blank", "issue-blank", "player-blank"} {
		if f, ok := iss.CoverFiles[key]; ok && f != "" {
			coverFile = f
			break
		}
	}
	if coverFile == "" {
		return ""
	}

	if iss.CoverMigrated == 1 && iss.Prefix != "" {
		return siteBase + "/api/covers/" + iss.Prefix + "/" + coverFile + "_900.jpg"
	}

	if !iss.Date().IsZero() {
		ymd := iss.Date().Format("20060102")
		return siteBase + "/api/legacy-covers/production/" + ymd + "-issue-cover-900.jpg"
	}

	return ""
}

func (iss issue) Date() time.Time {
	if t, err := time.Parse(time.RFC3339, iss.Datetime); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse("2006-01-02T15:04:05.000Z", iss.Datetime); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
