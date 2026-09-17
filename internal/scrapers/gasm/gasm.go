package gasm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const cdnBase = "https://c741b0f4ef.mjedge.net/"

// siteBase is a var so tests can point the scraper at an httptest server.
var siteBase = "https://www.gasm.com"

var domainToSlug = map[string]string{
	"buttformation.com":      "buttformation",
	"cosplaybabes.xxx":       "cosplaybabes",
	"filthyandfisting.com":   "filthyandfisting",
	"herzogvideo.de":         "herzogvideos",
	"harmonyvision.com":      "harmonyvision",
	"hotgold.xxx":            "hotgold",
	"inflagranti.com":        "inflagranti",
	"mmvfilms.com":           "mmvfilms",
	"mmvfilms.de":            "mmvfilms",
	"magmafilm.com":          "magmafilm",
	"paradise-films.com":     "paradisefilms",
	"purexxxfilms.com":       "purexxxfilms",
	"theundercoverlover.com": "theundercoverlover",
}

var profileRe = regexp.MustCompile(`(?i)^https?://(?:www\.)?gasm\.com/studio/profile/([\w-]+)`)

type Scraper struct {
	client *http.Client
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func New() *Scraper {
	jar, _ := cookiejar.New(nil)
	c := httpx.NewClient(30 * time.Second)
	c.Jar = jar
	return &Scraper{client: c}
}

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "gasm" }

func (s *Scraper) Patterns() []string {
	return []string{
		"gasm.com/studio/profile/{slug}",
		"buttformation.com",
		"cosplaybabes.xxx",
		"filthyandfisting.com",
		"herzogvideo.de",
		"harmonyvision.com",
		"hotgold.xxx",
		"inflagranti.com",
		"mmvfilms.com",
		"magmafilm.com",
		"paradise-films.com",
		"purexxxfilms.com",
		"theundercoverlover.com",
	}
}

func (s *Scraper) MatchesURL(u string) bool {
	if profileRe.MatchString(u) {
		return true
	}
	_, ok := domainToSlug[extractHost(u)]
	return ok
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	slug, err := resolveSlug(studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "gasm: resolved slug %q from URL %s", slug, studioURL)

	userID, videoCount, err := s.bootstrap(ctx, slug)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("bootstrap: %w", err)):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "gasm: studio %s (user_id=%d, videos=%d)", slug, userID, videoCount)

	if videoCount > 0 {
		select {
		case out <- scraper.Progress(videoCount):
		case <-ctx.Done():
			return
		}
	}

	work := make(chan listItem, opts.Workers)
	var wg sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				scene, fetchErr := s.fetchDetail(ctx, item.id, studioURL, slug)
				if fetchErr != nil {
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.id, fetchErr)):
					case <-ctx.Done():
						return
					}
					continue
				}
				if scene.Thumbnail == "" {
					scene.Thumbnail = item.thumbnail
				}
				if scene.Duration == 0 {
					scene.Duration = parseutil.ParseDurationColon(item.duration)
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	const perPage = 100
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && opts.Delay > 0 {
			cancelled := false
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				cancelled = true
			}
			if cancelled {
				break
			}
		}
		scraper.Debugf(1, "gasm: fetching page %d", page)

		items, totalPages, fetchErr := s.fetchListing(ctx, slug, userID, page, perPage)
		if fetchErr != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("listing page %d: %w", page, fetchErr)):
			case <-ctx.Done():
			}
			break
		}
		if len(items) == 0 {
			break
		}

		cancelled := false
		hitKnown := false
		for _, item := range items {
			if opts.KnownIDs[item.id] {
				hitKnown = true
				break
			}
			select {
			case work <- item:
			case <-ctx.Done():
				cancelled = true
			}
			if cancelled {
				break
			}
		}
		if cancelled || hitKnown {
			if hitKnown {
				scraper.Debugf(1, "gasm: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
			}
			break
		}
		if totalPages > 0 && page >= totalPages {
			break
		}
	}

	close(work)
	wg.Wait()
}

var (
	ajaxParamsRe = regexp.MustCompile(`data-ajax-params="([^"]+)"`)
	videoCountRe = regexp.MustCompile(`data-tab="videos">\s*VIDEOS\s*<span>\((\d+)\)</span>`)
)

type ajaxParams struct {
	UserIDs []int `json:"user_ids"`
}

func (s *Scraper) bootstrap(ctx context.Context, slug string) (userID, videoCount int, err error) {
	profileURL := siteBase + "/studio/profile/" + slug
	body, err := s.fetchHTML(ctx, profileURL)
	if err != nil {
		return 0, 0, err
	}
	return parseProfile(body, slug)
}

// sfwGateRe recognises the SFW preview the network serves in place of the whole
// site to visitors in jurisdictions with age-verification laws. It is not a
// parse failure, and saying "data-ajax-params not found" sends the reader
// looking for a broken regex instead of a geo block.
var sfwGateRe = regexp.MustCompile(`(?i)sfw\.gasm\.com|Age Verification laws`)

func parseProfile(body []byte, slug string) (userID, videoCount int, err error) {
	m := ajaxParamsRe.FindSubmatch(body)
	if m == nil {
		if sfwGateRe.Match(body) {
			return 0, 0, fmt.Errorf("the network served its SFW preview instead of %s: "+
				"this jurisdiction is age-verification blocked, not a parser problem", slug)
		}
		return 0, 0, fmt.Errorf("data-ajax-params not found on profile page for %s", slug)
	}
	decoded := html.UnescapeString(string(m[1]))
	var params ajaxParams
	if err := json.Unmarshal([]byte(decoded), &params); err != nil {
		return 0, 0, fmt.Errorf("parsing ajax params for %s: %w", slug, err)
	}
	if len(params.UserIDs) == 0 {
		return 0, 0, fmt.Errorf("no user_ids in ajax params for %s", slug)
	}
	userID = params.UserIDs[0]

	if m := videoCountRe.FindSubmatch(body); m != nil {
		videoCount, _ = strconv.Atoi(string(m[1]))
	}
	return userID, videoCount, nil
}

var (
	postIDRe   = regexp.MustCompile(`data-post-id="(\d+)"`)
	titleRe    = regexp.MustCompile(`class="post_title"[^>]*title="([^"]*)"`)
	posterRe   = regexp.MustCompile(`data-media-poster="([^"]+)"`)
	durationRe = regexp.MustCompile(`<b>(\d+:\d+)</b>`)
	lastPageRe = regexp.MustCompile(`data-page="(\d+)"[^>]*title="last"`)
)

type listItem struct {
	id        string
	title     string
	thumbnail string
	duration  string
}

func (s *Scraper) fetchListing(ctx context.Context, slug string, userID, page, perPage int) ([]listItem, int, error) {
	form := url.Values{
		"aParams[contentType]":              {"posts"},
		"aParams[user_ids][0]":              {strconv.Itoa(userID)},
		"aParams[sorting]":                  {"date_and_id"},
		"aParams[max_results]":              {strconv.Itoa(perPage)},
		"aParams[page]":                     {strconv.Itoa(page)},
		"aParams[aStatus][0]":               {"0"},
		"aParams[content_type][0]":          {"4"},
		"aParams[aVisibility][0]":           {"0"},
		"aParams[aVisibility][1]":           {"1"},
		"aParams[show_hidden]":              {"false"},
		"aParams[ajax_pagination]":          {"true"},
		"aParams[hide_x_rated_content]":     {"false"},
		"aParams[show_from_disabled_users]": {"false"},
		"aParams[show_pagination]":          {"true"},
		"aParams[item_template]":            {"MMFrontendBundle:www.gasm.com:item/post.html.twig"},
		"aParams[results_template]":         {"@MMCore/ContentBlock/results.html.twig"},
		"aParams[pagination_template]":      {"@MMCore/ContentBlock/pagination.html.twig"},
		"aParams[container_id]":             {"1"},
	}

	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		Method: http.MethodPost,
		URL:    siteBase + "/op/results/paginate",
		Body:   []byte(form.Encode()),
		Headers: map[string]string{
			"Content-Type":     "application/x-www-form-urlencoded",
			"X-Requested-With": "XMLHttpRequest",
			"User-Agent":       httpx.UserAgentFirefox,
			"Referer":          siteBase + "/studio/profile/" + slug,
		},
	})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	return parseListing(body)
}

func parseListing(body []byte) ([]listItem, int, error) {
	totalPages := 0
	if m := lastPageRe.FindSubmatch(body); m != nil {
		totalPages, _ = strconv.Atoi(string(m[1]))
	}

	cards := splitCards(body)
	items := make([]listItem, 0, len(cards))
	seen := make(map[string]bool, len(cards))

	for _, card := range cards {
		m := postIDRe.FindSubmatch(card)
		if m == nil {
			continue
		}
		id := string(m[1])
		if seen[id] {
			continue
		}
		seen[id] = true

		item := listItem{id: id}
		if m := titleRe.FindSubmatch(card); m != nil {
			item.title = html.UnescapeString(string(m[1]))
		}
		if m := posterRe.FindSubmatch(card); m != nil {
			item.thumbnail = html.UnescapeString(string(m[1]))
		}
		if m := durationRe.FindSubmatch(card); m != nil {
			item.duration = string(m[1])
		}
		items = append(items, item)
	}

	return items, totalPages, nil
}

var cardSep = []byte(`<div class="post_item video"`)

func splitCards(body []byte) [][]byte {
	parts := bytes.Split(body, cardSep)
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}

type devSpanData struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Body        string `json:"body"`
	PublishDate *struct {
		Date string `json:"date"`
	} `json:"publishdate"`
	Duration json.RawMessage `json:"duration"`
	Cover    string          `json:"cover"`
	Owner    struct {
		Username string `json:"username"`
	} `json:"owner"`
	Actors []struct {
		Name string `json:"name"`
	} `json:"actors"`
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags"`
}

var devSpanRe = regexp.MustCompile(`dev-span="([^"]+)"`)

func (s *Scraper) fetchDetail(ctx context.Context, id, studioURL, slug string) (models.Scene, error) {
	detailURL := siteBase + "/post/details/" + id
	body, err := s.fetchHTML(ctx, detailURL)
	if err != nil {
		return models.Scene{}, err
	}
	return parseDetail(body, id, studioURL, slug)
}

func parseDetail(body []byte, id, studioURL, slug string) (models.Scene, error) {
	now := time.Now().UTC()
	scene := models.Scene{
		ID:        id,
		SiteID:    "gasm",
		StudioURL: studioURL,
		URL:       "https://www.gasm.com/post/details/" + id,
		Studio:    slug,
		ScrapedAt: now,
	}

	m := devSpanRe.FindSubmatch(body)
	if m == nil {
		return scene, fmt.Errorf("dev-span not found on post %s", id)
	}

	decoded := html.UnescapeString(string(m[1]))
	var data devSpanData
	if err := json.Unmarshal([]byte(decoded), &data); err != nil {
		return scene, fmt.Errorf("dev-span JSON: %w", err)
	}

	scene.Title = data.Title
	scene.Description = strings.TrimSpace(data.Body)

	if data.PublishDate != nil && data.PublishDate.Date != "" {
		// The fractional part varies in width, so the layouts are tried in
		// order rather than pinned to six digits.
		if t, err := parseutil.TryParseDate(data.PublishDate.Date,
			"2006-01-02 15:04:05.000000", "2006-01-02 15:04:05", "2006-01-02"); err == nil {
			scene.Date = t.UTC()
		}
	}

	var dur string
	if err := json.Unmarshal(data.Duration, &dur); err == nil && dur != "" {
		scene.Duration = parseutil.ParseDurationColon(dur)
	}

	if data.Cover != "" {
		scene.Thumbnail = cdnBase + data.Cover
	}

	for _, a := range data.Actors {
		if name := strings.TrimSpace(a.Name); name != "" {
			scene.Performers = append(scene.Performers, name)
		}
	}

	for _, t := range data.Tags {
		if name := strings.TrimSpace(t.Name); name != "" {
			scene.Tags = append(scene.Tags, name)
		}
	}

	return scene, nil
}

func (s *Scraper) fetchHTML(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func resolveSlug(studioURL string) (string, error) {
	if m := profileRe.FindStringSubmatch(studioURL); m != nil {
		return m[1], nil
	}
	host := extractHost(studioURL)
	if slug, ok := domainToSlug[host]; ok {
		return slug, nil
	}
	return "", fmt.Errorf("cannot resolve GASM studio slug from URL: %s", studioURL)
}

func extractHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	return host
}
