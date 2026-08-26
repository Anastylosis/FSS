package bellesa

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
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

// Scraper reads the Bellesa listing pages. Every field the site publishes for a
// scene is already in the listing payload, so there is no detail fetch.
type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(45 * time.Second),
		base:   "https://www.bellesa.co",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "bellesa" }

func (s *Scraper) Patterns() []string {
	return []string{
		"bellesa.co/videos?providers={handle}",
		"bellesa.co/channels/{handle}",
		"bellesaplus.co/videos?providers={handle}",
		"bellesaplus.co/studio-preview/{studio}/videos?providers={handle}",
	}
}

var channelPathRe = regexp.MustCompile(`^/channels/([^/]+)/?$`)

func (s *Scraper) MatchesURL(u string) bool {
	return providerHandle(u) != ""
}

// providerHandle returns the content provider a studio URL selects, or "" if
// the URL names none. Bellesa serves the same catalogue on two hosts —
// bellesa.co is public, bellesaplus.co sits behind a Cloudflare challenge — and
// both spell the selection as a `providers` query parameter. The site's own
// /channels/{handle} pages redirect to that form.
func providerHandle(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" {
		return ""
	}
	if !scraper.HostMatches(rawURL, "bellesa.co", "bellesaplus.co") {
		return ""
	}
	if h := u.Query().Get("providers"); h != "" {
		return normalizeHandle(h)
	}
	if m := channelPathRe.FindStringSubmatch(u.Path); m != nil {
		if h, err := url.PathUnescape(m[1]); err == nil {
			return normalizeHandle(h)
		}
	}
	return ""
}

// normalizeHandle folds the spellings the site's own sitemap uses for one
// handle. The channel sitemap still lists the space-separated form ("bellesa
// house party"), which the listing no longer answers to; the hyphenated form is
// what returns scenes.
func normalizeHandle(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.ReplaceAll(h, " ", "-")
	h = strings.ReplaceAll(h, "+", "-")
	if strings.ContainsAny(h, "/?#") {
		return ""
	}
	return h
}

// PreferredStudioURL collapses the bellesaplus.co and /channels/ spellings of a
// provider onto the public listing URL, so the same catalogue reached three
// ways is stored once.
func (s *Scraper) PreferredStudioURL(studioURL string) string {
	h := providerHandle(studioURL)
	if h == "" {
		return ""
	}
	return "https://www.bellesa.co/videos?providers=" + url.QueryEscape(h)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	handle := providerHandle(studioURL)
	if handle == "" {
		return nil, fmt.Errorf("cannot extract bellesa provider from %q", studioURL)
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, handle, opts, out)
	return out, nil
}

// ---- payload types ----

type initialData struct {
	Videos     []apiVideo `json:"videos"`
	Pagination struct {
		Page  int `json:"page"`
		Pages int `json:"pages"`
		Total int `json:"total"`
	} `json:"pagination"`
}

// lastPage reports whether the payload says this is the final page. The page
// number the response echoes is preferred over the one asked for, so a listing
// that clamps an out-of-range page still terminates the loop.
func (d *initialData) lastPage(asked int) bool {
	if d.Pagination.Pages <= 0 {
		return false
	}
	page := d.Pagination.Page
	if page <= 0 {
		page = asked
	}
	return page >= d.Pagination.Pages
}

type apiVideo struct {
	ID          int    `json:"id"`
	PostedOn    int64  `json:"posted_on"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Tags        string `json:"tags"`
	Duration    int    `json:"duration"`
	Image       string `json:"image"`
	Preview     string `json:"preview"`
	Resolutions string `json:"resolutions"`
	Views       int    `json:"views"`
	Likes       int    `json:"likes"`
	Categories  []struct {
		Name string `json:"name"`
	} `json:"categories"`
	ContentProvider []struct {
		Name   string `json:"name"`
		Handle string `json:"handle"`
	} `json:"content_provider"`
	Performers []struct {
		Name string `json:"name"`
	} `json:"performers"`
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL, handle string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "bellesa: listing provider %q", handle)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, "bellesa", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/videos?providers=%s&page=%d", s.base, url.QueryEscape(handle), page)
		data, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, 0, len(data.Videos))
		for _, v := range data.Videos {
			scenes = append(scenes, toScene(studioURL, v, now))
		}
		return scraper.PageResult{Scenes: scenes, Total: data.Pagination.Total, Done: data.lastPage(page)}, nil
	})
}

// initialDataRe locates the server-rendered state the listing page carries. The
// page is a React app: the video list is in this blob, not in the markup.
var initialDataRe = regexp.MustCompile(`window\.__INITIAL_DATA__\s*=\s*`)

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) (*initialData, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading page: %w", err)
	}
	data, err := parseInitialData(body)
	if err != nil {
		return nil, scraper.ParseError(pageURL, err)
	}
	return data, nil
}

// parseInitialData extracts window.__INITIAL_DATA__ from a listing page. The
// blob is followed by more script on the same line, so its extent is found by
// balancing braces rather than by a terminator.
func parseInitialData(body []byte) (*initialData, error) {
	loc := initialDataRe.FindIndex(body)
	if loc == nil {
		return nil, fmt.Errorf("no __INITIAL_DATA__ on page")
	}
	rest := body[loc[1]:]
	end, ok := objectEnd(rest)
	if !ok {
		return nil, fmt.Errorf("unterminated __INITIAL_DATA__ object")
	}
	var data initialData
	if err := json.Unmarshal(rest[:end], &data); err != nil {
		return nil, fmt.Errorf("decoding __INITIAL_DATA__: %w", err)
	}
	return &data, nil
}

// objectEnd returns the index just past the JSON object starting at b[0],
// skipping braces that appear inside strings.
func objectEnd(b []byte) (int, bool) {
	if len(b) == 0 || b[0] != '{' {
		return 0, false
	}
	depth, inStr, esc := 0, false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

// ---- scene building ----

func toScene(studioURL string, v apiVideo, now time.Time) models.Scene {
	title := strings.TrimSpace(html.UnescapeString(v.Title))
	sc := models.Scene{
		ID:          strconv.Itoa(v.ID),
		SiteID:      "bellesa",
		StudioURL:   studioURL,
		Title:       title,
		URL:         sceneURL(v.ID, title),
		Description: strings.TrimSpace(html.UnescapeString(v.Description)),
		Thumbnail:   v.Image,
		Preview:     v.Preview,
		Duration:    v.Duration,
		Resolution:  topResolution(v.Resolutions),
		Views:       v.Views,
		Likes:       v.Likes,
		ScrapedAt:   now,
	}
	if v.PostedOn > 0 {
		sc.Date = time.Unix(v.PostedOn, 0).UTC()
	}
	for _, p := range v.Performers {
		if n := strings.TrimSpace(p.Name); n != "" {
			sc.Performers = append(sc.Performers, n)
		}
	}
	for _, c := range v.Categories {
		if n := strings.TrimSpace(c.Name); n != "" {
			sc.Categories = append(sc.Categories, n)
		}
	}
	sc.Tags = splitTags(v.Tags)
	if len(v.ContentProvider) > 0 {
		sc.Studio = strings.TrimSpace(v.ContentProvider[0].Name)
	}
	return sc
}

// sceneURL rebuilds the canonical scene URL. The listing payload carries no
// slug, but the site derives one from the title: lowercased, apostrophes
// dropped, every other run of non-alphanumerics collapsed to a hyphen.
func sceneURL(id int, title string) string {
	slug := slugify(title)
	if slug == "" {
		return fmt.Sprintf("https://www.bellesa.co/videos/%d", id)
	}
	return fmt.Sprintf("https://www.bellesa.co/videos/%d/%s", id, slug)
}

var nonSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(title string) string {
	t := strings.ToLower(title)
	t = strings.NewReplacer("'", "", "’", "", "‘", "").Replace(t)
	return strings.Trim(nonSlugRe.ReplaceAllString(t, "-"), "-")
}

// splitTags turns the comma-separated tag string into a deduplicated list. The
// site folds performer names and studio branding into the same field, which is
// stored as published.
func splitTags(raw string) []string {
	if raw == "" {
		return nil
	}
	seen := make(map[string]bool)
	var tags []string
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(html.UnescapeString(t))
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if seen[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, t)
	}
	return tags
}

// topResolution reports the highest rendition the site lists, as a height in
// pixels ("1080"). The field is a comma-separated ladder ("360,480,720,1080").
func topResolution(raw string) string {
	best := 0
	for _, r := range strings.Split(raw, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(r)); err == nil && n > best {
			best = n
		}
	}
	if best == 0 {
		return ""
	}
	return strconv.Itoa(best)
}
