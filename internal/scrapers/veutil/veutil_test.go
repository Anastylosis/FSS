package veutil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func testScraper(siteBase string) *Scraper {
	return &Scraper{
		cfg: SiteConfig{
			ID:             "mypervmom",
			Studio:         "PervMom",
			SiteBase:       siteBase,
			MainCategoryID: 1,
			MatchRe:        regexp.MustCompile(`^https?://(?:www\.)?mypervmom\.com(/|$)`),
		},
	}
}

func TestMatchesURL(t *testing.T) {
	s := testScraper("https://mypervmom.com")
	cases := []struct {
		url  string
		want bool
	}{
		{"https://mypervmom.com", true},
		{"https://mypervmom.com/", true},
		{"https://www.mypervmom.com/some-scene", true},
		{"https://example.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestExtractPoster(t *testing.T) {
	cases := []struct {
		content, want string
	}{
		{`<video poster="https://cdn.example.com/thumb.jpg"><source src="video.mp4"></video>`, "https://cdn.example.com/thumb.jpg"},
		{`<p>no video here</p>`, ""},
	}
	for _, c := range cases {
		if got := extractPoster(c.content); got != c.want {
			t.Errorf("extractPoster(%q) = %q, want %q", c.content[:20], got, c.want)
		}
	}
}

func TestPostToScene(t *testing.T) {
	s := testScraper("https://mypervmom.com")
	tagMap := map[int]string{10: "Daisy Stone", 20: "Joshua Lewis"}
	p := wpPost{
		ID:      2319,
		DateGMT: "2026-04-26T11:37:34",
		Link:    "https://mypervmom.com/sex-therapy-at-home/",
		Title:   wpRendered{Rendered: "Sex Therapy At Home &#8211; S3:E2"},
		Content: wpRendered{Rendered: `<p><video poster="https://cdn.example.com/thumb.jpg"><source src="video.mp4"></video></p>`},
		Tags:    []int{10, 20},
	}

	scene := s.postToScene("https://mypervmom.com", p, tagMap, fixedTime())

	if scene.ID != "2319" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.Title != "Sex Therapy At Home – S3:E2" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.URL != "https://mypervmom.com/sex-therapy-at-home/" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Thumbnail != "https://cdn.example.com/thumb.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if scene.Date.Year() != 2026 || scene.Date.Month() != 4 || scene.Date.Day() != 26 {
		t.Errorf("Date = %v", scene.Date)
	}
	if len(scene.Performers) != 2 || scene.Performers[0] != "Daisy Stone" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if scene.Studio != "PervMom" {
		t.Errorf("Studio = %q", scene.Studio)
	}
}

func newTestServer(tags []wpTag, posts []wpPost) *httptest.Server {
	return newTestServerTagged(tags, posts, nil)
}

func newTestServerTagged(tags []wpTag, posts []wpPost, taggedPosts map[string][]wpPost) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wp-json/wp/v2/tags":
			if slug := r.URL.Query().Get("slug"); slug != "" {
				for _, t := range tags {
					if t.Slug == slug {
						_ = json.NewEncoder(w).Encode([]wpTag{t})
						return
					}
				}
				_, _ = w.Write([]byte("[]"))
				return
			}
			w.Header().Set("X-WP-Total", "2")
			_ = json.NewEncoder(w).Encode(tags)
		case "/wp-json/wp/v2/posts":
			page := r.URL.Query().Get("page")
			if page == "2" {
				w.Header().Set("X-WP-Total", "0")
				_, _ = w.Write([]byte("[]"))
				return
			}
			if tagID := r.URL.Query().Get("tags"); tagID != "" {
				if tp, ok := taggedPosts[tagID]; ok {
					w.Header().Set("X-WP-Total", itoa(len(tp)))
					_ = json.NewEncoder(w).Encode(tp)
					return
				}
				w.Header().Set("X-WP-Total", "0")
				_, _ = w.Write([]byte("[]"))
				return
			}
			w.Header().Set("X-WP-Total", itoa(len(posts)))
			_ = json.NewEncoder(w).Encode(posts)
		default:
			http.NotFound(w, r)
		}
	}))
}

func itoa(n int) string {
	return string(rune('0' + n))
}

func TestListScenes(t *testing.T) {
	tags := []wpTag{{ID: 10, Name: "Daisy Stone"}}
	posts := []wpPost{
		{
			ID: 100, DateGMT: "2026-04-26T11:00:00",
			Link:    "https://mypervmom.com/scene-one/",
			Title:   wpRendered{Rendered: "Scene One"},
			Content: wpRendered{Rendered: `<video poster="https://cdn.example.com/1.jpg"></video>`},
			Tags:    []int{10},
		},
		{
			ID: 99, DateGMT: "2026-04-25T10:00:00",
			Link:    "https://mypervmom.com/scene-two/",
			Title:   wpRendered{Rendered: "Scene Two"},
			Content: wpRendered{Rendered: `<video poster="https://cdn.example.com/2.jpg"></video>`},
			Tags:    []int{10},
		},
	}

	ts := newTestServer(tags, posts)
	defer ts.Close()

	s := &Scraper{
		cfg: SiteConfig{
			ID: "mypervmom", Studio: "PervMom", SiteBase: ts.URL,
			MainCategoryID: 1, MatchRe: regexp.MustCompile(`.*`),
		},
		Client: ts.Client(),
	}

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 2 || scenes[0].Title != "Scene One" || scenes[1].Title != "Scene Two" {
		t.Errorf("got %d scenes", len(scenes))
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	tags := []wpTag{{ID: 10, Name: "Actor"}}
	posts := []wpPost{
		{ID: 200, DateGMT: "2026-04-26T11:00:00", Link: "https://mypervmom.com/new/", Title: wpRendered{Rendered: "New"}, Tags: []int{10}},
		{ID: 199, DateGMT: "2026-04-25T10:00:00", Link: "https://mypervmom.com/known/", Title: wpRendered{Rendered: "Known"}, Tags: []int{10}},
	}

	ts := newTestServer(tags, posts)
	defer ts.Close()

	s := &Scraper{
		cfg: SiteConfig{
			ID: "mypervmom", Studio: "PervMom", SiteBase: ts.URL,
			MainCategoryID: 1, MatchRe: regexp.MustCompile(`.*`),
		},
		Client: ts.Client(),
	}

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"199": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, ch)

	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
	if len(scenes) != 1 || scenes[0].Title != "New" {
		t.Errorf("got %d scenes, want [New]", len(scenes))
	}
}

func TestListScenesTagFilter(t *testing.T) {
	tags := []wpTag{
		{ID: 10, Name: "Daisy Stone", Slug: "daisy-stone"},
		{ID: 20, Name: "Joshua Lewis", Slug: "joshua-lewis"},
	}
	allPosts := []wpPost{
		{ID: 100, DateGMT: "2026-04-26T11:00:00", Link: "/scene-one/", Title: wpRendered{Rendered: "Scene One"}, Tags: []int{10, 20}},
		{ID: 99, DateGMT: "2026-04-25T10:00:00", Link: "/scene-two/", Title: wpRendered{Rendered: "Scene Two"}, Tags: []int{20}},
	}
	taggedPosts := map[string][]wpPost{
		"10": {allPosts[0]},
	}

	ts := newTestServerTagged(tags, allPosts, taggedPosts)
	defer ts.Close()

	s := &Scraper{
		cfg: SiteConfig{
			ID: "mypervmom", Studio: "PervMom", SiteBase: ts.URL,
			MainCategoryID: 1, MatchRe: regexp.MustCompile(`.*`),
		},
		Client: ts.Client(),
	}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/tag/daisy-stone/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if scenes[0].Title != "Scene One" {
		t.Errorf("Title = %q, want %q", scenes[0].Title, "Scene One")
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build wpPost values in Go, so encode and decode share the
// struct tag and a renamed tag round-trips unnoticed. This one decodes a
// wp-json response captured verbatim from a live video-elements site and runs a
// post through postToScene.
//
// The WordPress shape is the interesting part: `title` and `content` are not
// strings but `{"rendered": …}` wrappers, `tags` is a list of numeric IDs that
// must be resolved separately, and the date field is `date_gmt` rather than
// `date` — three easy things to model wrong.
func TestGoldenPosts(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "posts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var posts []wpPost
	if err := json.Unmarshal(body, &posts); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("decoded %d posts, want 2", len(posts))
	}

	p := posts[0]
	if p.ID != 2150 {
		t.Errorf("ID = %d (id), want 2150", p.ID)
	}
	if p.DateGMT != "2026-07-24T00:00:00" {
		t.Errorf("DateGMT = %q (date_gmt, not date)", p.DateGMT)
	}
	if p.Link == "" {
		t.Error("Link is empty (link)")
	}
	// title/content are {"rendered": …} wrappers, not plain strings.
	if p.Title.Rendered != "The Undress Rehearsal" {
		t.Errorf("Title.Rendered = %q (title.rendered)", p.Title.Rendered)
	}
	if p.Content.Rendered == "" {
		t.Error("Content.Rendered is empty (content.rendered)")
	}

	s := New(SiteConfig{ID: "brattyfamily", SiteBase: "https://brattyfamily.com", Studio: "Bratty Family"})
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	sc := s.postToScene("https://brattyfamily.com", p, map[int]string{}, now)

	if sc.ID != "2150" {
		t.Errorf("scene ID = %q", sc.ID)
	}
	if sc.Title != "The Undress Rehearsal" {
		t.Errorf("scene Title = %q", sc.Title)
	}
	if sc.Date.Format("2006-01-02") != "2026-07-24" {
		t.Errorf("scene Date = %v, want 2026-07-24 (date_gmt)", sc.Date)
	}
	if !strings.Contains(sc.URL, "the-undress-rehearsal") {
		t.Errorf("scene URL = %q (link)", sc.URL)
	}
}

// Only the 400 past the end means the tag list is complete. Returning a partial
// map with a nil error is worse than it sounds: no scene is dropped, so nothing
// looks wrong, but every unmapped tag ID resolves to nothing and
// `--full`/`--refresh` overwrite complete stored tag lists with truncated ones
// across every veutil site.
func TestFetchAllTagsLatePageFailureIsFatal(t *testing.T) {
	for _, tt := range []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"past the end", http.StatusBadRequest, false},
		{"rate limited", http.StatusTooManyRequests, true},
		{"bad gateway", http.StatusBadGateway, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/wp-json/wp/v2/tags" {
					http.NotFound(w, r)
					return
				}
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if page > 1 {
					w.WriteHeader(tt.status)
					return
				}
				out := make([]wpTag, 0, postsPerPage)
				for i := 0; i < postsPerPage; i++ {
					out = append(out, wpTag{ID: 100 + i, Name: "Tag"})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer ts.Close()

			s := &Scraper{
				cfg:    SiteConfig{ID: "mypervmom", Studio: "PervMom", SiteBase: ts.URL, MatchRe: regexp.MustCompile(`.*`)},
				Client: ts.Client(),
			}
			got, err := s.fetchAllTags(context.Background())
			if tt.wantErr {
				if err == nil {
					t.Errorf("HTTP %d on page 2 returned %d tags and no error — a truncated map that looks complete", tt.status, len(got))
				}
				return
			}
			if err != nil {
				t.Errorf("HTTP %d on page 2 is the end of the list, not an error: %v", tt.status, err)
			}
			if len(got) != postsPerPage {
				t.Errorf("got %d tags, want %d", len(got), postsPerPage)
			}
		})
	}
}
