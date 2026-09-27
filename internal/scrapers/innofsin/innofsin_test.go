package innofsin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

const postsJSON = `[
  {
    "id": 100,
    "date": "2026-05-15T12:00:00",
    "slug": "test-scene",
    "link": "BASEURL/test-scene/",
    "title": {"rendered": "Test Scene Title"},
    "content": {"rendered": "<p>A great scene with action.</p>"},
    "featured_media": 101,
    "categories": [1, 2],
    "_embedded": {
      "wp:featuredmedia": [{"source_url": "https://cdn.example.com/thumb.jpg"}],
      "wp:term": [[
        {"id": 1, "name": "Interracial"},
        {"id": 2, "name": "Scenes"}
      ]]
    }
  },
  {
    "id": 99,
    "date": "2026-05-10T10:30:00",
    "slug": "another-scene",
    "link": "BASEURL/another-scene/",
    "title": {"rendered": "Another Scene"},
    "content": {"rendered": "<p><b>Another</b> description here.</p>"},
    "featured_media": 102,
    "categories": [3],
    "_embedded": {
      "wp:featuredmedia": [{"source_url": "https://cdn.example.com/thumb2.jpg"}],
      "wp:term": [[
        {"id": 3, "name": "Anal"}
      ]]
    }
  }
]`

const detailHTML = `<html><body>
<div class="performers">
  <a href="/pornstars/jane-doe/">Jane Doe</a>
  <a href="/pornstars/alice-smith/">Alice Smith</a>
</div>
</body></html>`

func newTestServer() *httptest.Server {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/wp/v2/posts":
			w.Header().Set("X-WP-Total", "2")
			w.Header().Set("X-WP-TotalPages", "1")
			body := strings.ReplaceAll(postsJSON, "BASEURL", ts.URL)
			_, _ = fmt.Fprint(w, body)
		default:
			_, _ = fmt.Fprint(w, detailHTML)
		}
	}))
	return ts
}

func TestFetchPage(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := &siteScraper{
		cfg:    sites[0],
		client: ts.Client(),
	}

	posts, total, totalPages, err := s.fetchPage(context.Background(), ts.URL+"/wp-json/wp/v2/posts?per_page=100&page=1&_embed")
	if err != nil {
		t.Fatalf("fetchPage: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	_ = totalPages
	if len(posts) != 2 {
		t.Fatalf("got %d posts, want 2", len(posts))
	}
	if posts[0].ID != 100 {
		t.Errorf("posts[0].ID = %d, want 100", posts[0].ID)
	}
	if posts[0].Title.Rendered != "Test Scene Title" {
		t.Errorf("title = %q", posts[0].Title.Rendered)
	}
	if len(posts[0].Embedded.Media) == 0 {
		t.Fatal("no embedded media")
	}
	if posts[0].Embedded.Media[0].SourceURL != "https://cdn.example.com/thumb.jpg" {
		t.Errorf("thumbnail = %q", posts[0].Embedded.Media[0].SourceURL)
	}
}

func TestFetchPerformers(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := &siteScraper{
		cfg:    sites[0],
		client: ts.Client(),
	}

	performers, err := s.fetchPerformers(context.Background(), ts.URL+"/test-scene/")
	if err != nil {
		t.Fatalf("fetchPerformers: %v", err)
	}
	if len(performers) != 2 {
		t.Fatalf("got %d performers, want 2", len(performers))
	}
	if performers[0] != "Jane Doe" {
		t.Errorf("performers[0] = %q, want Jane Doe", performers[0])
	}
	if performers[1] != "Alice Smith" {
		t.Errorf("performers[1] = %q, want Alice Smith", performers[1])
	}
}

func TestBuildScene(t *testing.T) {
	post := wpPost{
		ID:      100,
		Date:    "2026-05-15T12:00:00",
		Slug:    "test-scene",
		Link:    "https://example.com/test-scene/",
		Title:   wpField{Rendered: "Test &amp; Scene"},
		Content: wpField{Rendered: "<p>A <b>great</b> scene.</p>"},
		Embedded: wpEmbed{
			Media: []wpMedia{{SourceURL: "https://cdn.example.com/thumb.jpg"}},
			Terms: [][]wpTerm{{
				{ID: 1, Name: "Interracial"},
				{ID: 2, Name: "Scenes"},
				{ID: 3, Name: "Anal"},
			}},
		},
	}
	performers := []string{"Jane Doe"}
	cfg := siteConfig{id: "testsite", studio: "Test Studio"}
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	scene := buildScene(post, performers, cfg, "https://example.com", now)

	if scene.ID != "100" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.Title != "Test & Scene" {
		t.Errorf("Title = %q", scene.Title)
	}
	wantDate := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", scene.Date, wantDate)
	}
	if scene.Thumbnail != "https://cdn.example.com/thumb.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if scene.Description != "A great scene." {
		t.Errorf("Description = %q", scene.Description)
	}
	// "Scenes" should be filtered out
	if len(scene.Tags) != 2 || scene.Tags[0] != "Interracial" || scene.Tags[1] != "Anal" {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Jane Doe" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if scene.Studio != "Test Studio" {
		t.Errorf("Studio = %q", scene.Studio)
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := &siteScraper{
		cfg:    sites[0],
		client: ts.Client(),
	}

	ctx := context.Background()
	ch, err := s.ListScenes(ctx, ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var sceneCount int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			sceneCount++
			if r.Scene.ID == "" {
				t.Error("scene has empty ID")
			}
			if r.Scene.Title == "" {
				t.Error("scene has empty Title")
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if sceneCount != 2 {
		t.Errorf("got %d scenes, want 2", sceneCount)
	}
}

func TestMatchesURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://mydeepdarksecret.com/", "mydeepdarksecret"},
		{"https://www.richardmannsworld.com/", "richardmannsworld"},
		{"https://bbctitans.com", "bbctitans"},
		{"https://www.richardmannevents.com/", "richardmannevents"},
	}
	for _, tt := range tests {
		found := false
		for _, cfg := range sites {
			s := newScraper(cfg)
			if s.MatchesURL(tt.url) {
				if s.ID() != tt.want {
					t.Errorf("MatchesURL(%q) matched %q, want %q", tt.url, s.ID(), tt.want)
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no scraper matched %q", tt.url)
		}
	}
}

func TestStripHTML(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"<p>Hello <b>world</b></p>", "Hello world"},
		{"<p>Test &amp; stuff</p>", "Test & stuff"},
		{"plain text", "plain text"},
		{"  <span>  spaced  </span>  ", "spaced"},
	}
	for _, tt := range tests {
		got := stripHTML(tt.in)
		if got != tt.want {
			t.Errorf("stripHTML(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ---- exact-multiple-of-page-size regression ----
//
// When the post count is an exact multiple of the page size, the loop asks for
// one page past the end and WP answers HTTP 400. That error used to propagate
// out of fetchAllPosts, and the caller discarded every post already fetched —
// so the site was unscrapeable until its post count drifted off the boundary.

// exactMultipleServer serves `total` posts at `perPage` per page and answers
// HTTP 400 past the end, the way WordPress does.
func exactMultipleServer(t *testing.T, total, perPage int) *httptest.Server {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wp-json/wp/v2/posts" {
			_, _ = fmt.Fprint(w, detailHTML)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		totalPages := (total + perPage - 1) / perPage
		if page > totalPages {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"code":"rest_post_invalid_page_number"}`)
			return
		}

		start := (page - 1) * perPage
		end := min(start+perPage, total)
		var sb strings.Builder
		sb.WriteString("[")
		for i := start; i < end; i++ {
			if i > start {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `{"id":%d,"date":"2026-01-01T00:00:00","slug":"s%d","link":"%s/s%d/","title":{"rendered":"Scene %d"},"content":{"rendered":""}}`,
				1000+i, i, ts.URL, i, i)
		}
		sb.WriteString("]")

		w.Header().Set("X-WP-Total", strconv.Itoa(total))
		w.Header().Set("X-WP-TotalPages", strconv.Itoa(totalPages))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, sb.String())
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestFetchAllPostsExactMultiple(t *testing.T) {
	// 200 posts at 100/page: page 3 is past the end and 400s.
	ts := exactMultipleServer(t, 200, postsPerPage)
	s := &siteScraper{cfg: sites[0], client: ts.Client()}

	out := make(chan scraper.SceneResult, 16)
	posts, err := s.fetchAllPosts(context.Background(), ts.URL, scraper.ListOpts{}, out)
	if err != nil {
		t.Fatalf("fetchAllPosts returned an error on an exact page-size multiple: %v", err)
	}
	if len(posts) != 200 {
		t.Fatalf("got %d posts, want 200 — the past-the-end 400 must not discard them", len(posts))
	}
}

func TestFetchAllPostsPartialLastPage(t *testing.T) {
	ts := exactMultipleServer(t, 250, postsPerPage)
	s := &siteScraper{cfg: sites[0], client: ts.Client()}

	out := make(chan scraper.SceneResult, 16)
	posts, err := s.fetchAllPosts(context.Background(), ts.URL, scraper.ListOpts{}, out)
	if err != nil {
		t.Fatalf("fetchAllPosts: %v", err)
	}
	if len(posts) != 250 {
		t.Fatalf("got %d posts, want 250", len(posts))
	}
}

// A first-page failure is still a real error — it means the site is broken,
// not that the listing ended.
func TestFetchAllPostsFirstPageErrorIsFatal(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	s := &siteScraper{cfg: sites[0], client: ts.Client()}
	out := make(chan scraper.SceneResult, 16)
	if _, err := s.fetchAllPosts(context.Background(), ts.URL, scraper.ListOpts{}, out); err == nil {
		t.Error("expected an error when page 1 fails")
	}
}

// Domain-keyed config table — see testutil.CheckSiteDomainTable.
func TestSiteTableIntegrity(t *testing.T) {
	rows := make([]testutil.DomainRow, 0, len(sites))
	for _, c := range sites {
		rows = append(rows, testutil.DomainRow{ID: c.id, Domain: c.domain, Studio: c.studio})
	}
	testutil.CheckSiteDomainTable(t, rows)
}

// The 400 past the end is the *only* page>1 failure that means end-of-list.
// Treating any of them as the end returned the pages already fetched with a nil
// error, and under `--full` the authoritative Save then hard-deleted every
// scene the run never reached — reporting success. This is the test whose
// absence let that ship.
func TestFetchAllPostsLatePageFailureIsFatal(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		want   bool // want an error
	}{
		{"past the end", http.StatusBadRequest, false},
		{"rate limited", http.StatusTooManyRequests, true},
		{"bad gateway", http.StatusBadGateway, true},
		{"gone", http.StatusGone, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var ts *httptest.Server
			ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/wp-json/wp/v2/posts" {
					_, _ = fmt.Fprint(w, detailHTML)
					return
				}
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if page > 1 {
					w.WriteHeader(tt.status)
					return
				}
				var sb strings.Builder
				sb.WriteString("[")
				for i := 0; i < postsPerPage; i++ {
					if i > 0 {
						sb.WriteString(",")
					}
					fmt.Fprintf(&sb, `{"id":%d,"date":"2026-01-01T00:00:00","slug":"s%d","link":"%s/s%d/","title":{"rendered":"Scene %d"},"content":{"rendered":""}}`,
						1000+i, i, ts.URL, i, i)
				}
				sb.WriteString("]")
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, sb.String())
			}))
			defer ts.Close()

			s := &siteScraper{cfg: sites[0], client: ts.Client()}
			out := make(chan scraper.SceneResult, 16)
			_, err := s.fetchAllPosts(context.Background(), ts.URL, scraper.ListOpts{}, out)
			if tt.want && err == nil {
				t.Errorf("HTTP %d on page 2 returned no error — the caller would keep a truncated catalogue", tt.status)
			}
			if !tt.want && err != nil {
				t.Errorf("HTTP %d on page 2 is the end of the listing, not an error: %v", tt.status, err)
			}
		})
	}
}
