package fakings

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
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

func (s *Scraper) ID() string { return "fakings" }

func (s *Scraper) Patterns() []string {
	return []string{
		"fakings.com",
		"fakings.com/serie/{slug}",
		"fakings.com/actrices-porno/{slug}",
		"fakings.com/categoria/{slug}",
		"madlifes.com",
		"nigged.com",
		"pepeporn.com",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:fakings|madlifes|nigged|pepeporn)\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- URL routing ----

var (
	seriePath    = regexp.MustCompile(`/serie/([^/?#]+)`)
	actressPath  = regexp.MustCompile(`/actrices-porno/([^/?#]+)`)
	categoryPath = regexp.MustCompile(`/categoria/([^/?#]+)`)
	baseHostRe   = regexp.MustCompile(`^(https?://[^/]+)`)
)

type pageMode int

const (
	modeVideos pageMode = iota
	modeSerie
	modeActress
	modeCategory
)

type pageConfig struct {
	mode    pageMode
	baseURL string
}

func resolveConfig(rawURL string) pageConfig {
	base := "https://fakings.com"
	if m := baseHostRe.FindString(rawURL); m != "" {
		base = m
	}
	if m := seriePath.FindStringSubmatch(rawURL); m != nil {
		return pageConfig{mode: modeSerie, baseURL: base + "/serie/" + m[1]}
	}
	if m := actressPath.FindStringSubmatch(rawURL); m != nil {
		return pageConfig{mode: modeActress, baseURL: base + "/actrices-porno/" + m[1]}
	}
	if m := categoryPath.FindStringSubmatch(rawURL); m != nil {
		return pageConfig{mode: modeCategory, baseURL: base + "/categoria/" + m[1]}
	}
	return pageConfig{mode: modeVideos, baseURL: base + "/videos"}
}

func (pc pageConfig) pageURL(page int) string {
	if page <= 1 {
		return pc.baseURL
	}
	return pc.baseURL + "/f/pag:" + strconv.Itoa(page)
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	pc := resolveConfig(studioURL)
	if pc.mode == modeActress {
		s.runActress(ctx, pc, studioURL, opts, out)
		return
	}

	now := time.Now().UTC()
	var totalPages int
	scraper.Paginate(ctx, opts, "fakings", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchHTML(ctx, pc.pageURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}

		rsc := extractRSC(body)
		videos := parseGridVideos(rsc)
		if len(videos) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			t, take := parsePagination(rsc)
			total = t
			if take > 0 {
				totalPages = (t + take - 1) / take
			}
		}

		scenes := make([]models.Scene, len(videos))
		for i, v := range videos {
			scenes[i] = v.toScene(studioURL, now)
		}

		// An unreadable pagination block must not read as "last page": that
		// turned one regex miss into a one-page catalogue, which --full's
		// authoritative Save then treats as the whole studio. With totalPages
		// unknown the walk continues and ends on Paginate's own empty-page and
		// repeat-page guards instead.
		done := totalPages > 0 && page >= totalPages
		return scraper.PageResult{Scenes: scenes, Total: total, Done: done}, nil
	})
}

func (s *Scraper) runActress(ctx context.Context, pc pageConfig, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetchHTML(ctx, pc.baseURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	rsc := extractRSC(body)
	videos := parseActressVideos(rsc)

	if len(videos) > 0 {
		select {
		case out <- scraper.Progress(len(videos)):
		case <-ctx.Done():
			return
		}
	}

	var performer string
	if m := actressPath.FindStringSubmatch(pc.baseURL); m != nil {
		performer = titleCase(strings.ReplaceAll(m[1], "-", " "))
	}

	now := time.Now().UTC()
	for _, v := range videos {
		scene := v.toScene(studioURL, now)
		if performer != "" {
			scene.Performers = []string{performer}
		}
		if opts.KnownIDs[scene.ID] {
			scraper.Debugf(1, "fakings: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}
}

// ---- HTTP ----

func (s *Scraper) fetchHTML(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- RSC payload extraction ----

var rscChunkRe = regexp.MustCompile(`self\.__next_f\.push\(\[1,\s*"((?:[^"\\]|\\.)*)"\]\)`)

func extractRSC(html []byte) string {
	matches := rscChunkRe.FindAllSubmatch(html, -1)
	var sb strings.Builder
	for _, m := range matches {
		sb.Write(unescapeJS(m[1]))
	}
	return sb.String()
}

func unescapeJS(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); i++ {
		if b[i] == '\\' && i+1 < len(b) {
			switch b[i+1] {
			case '"':
				out = append(out, '"')
			case '\\':
				out = append(out, '\\')
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			default:
				out = append(out, b[i+1])
			}
			i++
		} else {
			out = append(out, b[i])
		}
	}
	return out
}

// ---- video JSON parsing ----

type rscVideo struct {
	ID              int       `json:"id"`
	Likes           int       `json:"likes"`
	Views           int       `json:"views"`
	Date            string    `json:"date"`
	Title           string    `json:"title"`
	Product         string    `json:"product"`
	Duration        string    `json:"duration"`
	Slug            string    `json:"slug"`
	Profile         string    `json:"profile"`
	Serie           *rscSerie `json:"serie"`
	PreviewFilename string    `json:"previewFilename"`
	Type            string    `json:"type"`
	Screenshot      string    `json:"screenshot"`
	StandardPhoto   string    `json:"standardPhoto"`
}

type rscSerie struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
}

const cdnBase = "https://almacen-faknetworks.b-cdn.net/videos/portada_"

func (v rscVideo) toScene(studioURL string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        strconv.Itoa(v.ID),
		SiteID:    "fakings",
		StudioURL: studioURL,
		Title:     v.Title,
		URL:       "https://fakings.com/video/" + v.Slug,
		Duration:  parseutil.ParseDurationColon(v.Duration),
		Views:     v.Views,
		Likes:     v.Likes,
		ScrapedAt: now,
	}
	if v.Date != "" {
		if t, err := time.Parse("2006-01-02", v.Date); err == nil {
			scene.Date = t.UTC()
		}
	}
	if v.Profile != "" {
		scene.Thumbnail = cdnBase + v.Profile
	}
	if v.Serie != nil && v.Serie.Title != "" {
		scene.Series = v.Serie.Title
	}
	if v.Product != "" {
		scene.Studio = v.Product
	}
	return scene
}

var videoKeyRe = regexp.MustCompile(`"video":\{`)

func parseGridVideos(rsc string) []rscVideo {
	var videos []rscVideo
	seen := make(map[int]bool)
	for _, loc := range videoKeyRe.FindAllStringIndex(rsc, -1) {
		objStart := loc[0] + len(`"video":`)
		obj := extractJSONObject(rsc, objStart)
		if obj == "" {
			continue
		}
		var v rscVideo
		if err := json.Unmarshal([]byte(obj), &v); err != nil || v.ID == 0 {
			continue
		}
		if seen[v.ID] {
			continue
		}
		seen[v.ID] = true
		videos = append(videos, v)
	}
	return videos
}

var videosArrayRe = regexp.MustCompile(`"videos":\[`)

func parseActressVideos(rsc string) []rscVideo {
	var videos []rscVideo
	seen := make(map[int]bool)
	for _, loc := range videosArrayRe.FindAllStringIndex(rsc, -1) {
		arrStart := loc[0] + len(`"videos":`)
		arr := extractJSONArray(rsc, arrStart)
		if arr == "" {
			continue
		}
		var vids []rscVideo
		if err := json.Unmarshal([]byte(arr), &vids); err != nil {
			continue
		}
		for _, v := range vids {
			if v.ID == 0 || seen[v.ID] {
				continue
			}
			seen[v.ID] = true
			videos = append(videos, v)
		}
	}
	return videos
}

// ---- pagination ----

var paginationRe = regexp.MustCompile(`"selectedPage":\d+[^}]*"total":(\d+),"take":(\d+)`)

func parsePagination(rsc string) (total, take int) {
	if m := paginationRe.FindStringSubmatch(rsc); m != nil {
		total, _ = strconv.Atoi(m[1])
		take, _ = strconv.Atoi(m[2])
	}
	return
}

// ---- JSON extraction helpers ----

func extractJSONObject(data string, start int) string {
	if start >= len(data) || data[start] != '{' {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(data); i++ {
		ch := data[i]
		if esc {
			esc = false
			continue
		}
		if ch == '\\' && inStr {
			esc = true
			continue
		}
		if ch == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return data[start : i+1]
			}
		}
	}
	return ""
}

func extractJSONArray(data string, start int) string {
	if start >= len(data) || data[start] != '[' {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(data); i++ {
		ch := data[i]
		if esc {
			esc = false
			continue
		}
		if ch == '\\' && inStr {
			esc = true
			continue
		}
		if ch == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch ch {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return data[start : i+1]
			}
		}
	}
	return ""
}

// ---- helpers ----

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}
