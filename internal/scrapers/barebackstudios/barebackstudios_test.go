package barebackstudios

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

const fixtureListing = `<html><body>
<div class="video_10 col-lg-3 col-md-6 col-sm-12 mb-2 px-2">
    <div class="thumbnail rounded overflow-hidden"
        data-video-id="10"
        data-video-url="https://cdn.example.com/preview.mp4"
        data-title="First Scene Title"
        data-summary="None"
        data-description="A test description for the first scene."
        data-created="2 months, 1 week ago"
        data-preview-gif="https://cdn.example.com/preview.gif"
        data-thumb="https://cdn.example.com/thumb1.png"
        data-keywords="Tag One, Tag Two, Tag Three"
        data-actors="Alice, Bob"
        data-category="Taboo, Milf">
        <div class="gif-container not-bought rounded-top">
            <div class="aspect-ratio-box preview-container">
                <img class="gif-player" src="placeholder.png" />
            </div>
        </div>
        <div class="p-2 video-info rounded-bottom">
            <div><small >$ 14.99</small></div>
        </div>
    </div>
</div>
<div class="video_9 col-lg-3 col-md-6 col-sm-12 mb-2 px-2">
    <div class="thumbnail rounded overflow-hidden"
        data-video-id="9"
        data-video-url="https://cdn.example.com/preview2.mp4"
        data-title="Second Scene"
        data-summary="None"
        data-description=""
        data-created="5 months ago"
        data-preview-gif="https://cdn.example.com/preview2.gif"
        data-thumb="https://cdn.example.com/thumb2.png"
        data-keywords="Solo"
        data-actors="Carol"
        data-category="Lifestyle">
        <div class="gif-container not-bought rounded-top">
            <div class="aspect-ratio-box preview-container">
                <img class="gif-player" src="placeholder.png" />
            </div>
        </div>
        <div class="p-2 video-info rounded-bottom">
            <div><small >$ 9.99</small></div>
        </div>
    </div>
</div>
</body></html>`

const fixtureEmpty = `<html><body></body></html>`

func TestParseListing(t *testing.T) {
	entries := parseListing(fixtureListing)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	e := entries[0]
	if e.id != "10" {
		t.Errorf("id = %q, want 10", e.id)
	}
	if e.title != "First Scene Title" {
		t.Errorf("title = %q", e.title)
	}
	if e.description != "A test description for the first scene." {
		t.Errorf("description = %q", e.description)
	}
	if e.thumb != "https://cdn.example.com/thumb1.png" {
		t.Errorf("thumb = %q", e.thumb)
	}
	if len(e.performers) != 2 || e.performers[0] != "Alice" || e.performers[1] != "Bob" {
		t.Errorf("performers = %v", e.performers)
	}
	if len(e.tags) != 3 || e.tags[0] != "Tag One" {
		t.Errorf("tags = %v", e.tags)
	}
	if len(e.categories) != 2 || e.categories[0] != "Taboo" {
		t.Errorf("categories = %v", e.categories)
	}
	if e.price != 14.99 {
		t.Errorf("price = %f, want 14.99", e.price)
	}
	if e.relDate != "2 months, 1 week ago" {
		t.Errorf("relDate = %q", e.relDate)
	}

	e2 := entries[1]
	if e2.id != "9" {
		t.Errorf("second id = %q, want 9", e2.id)
	}
	if e2.price != 9.99 {
		t.Errorf("second price = %f, want 9.99", e2.price)
	}
}

func TestParseRelativeDate(t *testing.T) {
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		input string
		want  string
	}{
		{"1 month, 1 week ago", "2025-05-08"},
		{"2 months, 3 weeks ago", "2025-03-25"},
		{"5 months ago", "2025-01-15"},
		{"1 year ago", "2024-06-15"},
		{"3 days ago", "2025-06-12"},
		{"1 year, 2 months ago", "2024-04-15"},
	}
	for _, tt := range tests {
		got := parseRelativeDate(tt.input, now).Format("2006-01-02")
		if got != tt.want {
			t.Errorf("parseRelativeDate(%q) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func newTestServer(pages map[int]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/site/verify_age/":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"status":"success"}`)
			return
		case "/en/":
			page := r.URL.Query().Get("page")
			if page == "" {
				page = "1"
			}
			for p, body := range pages {
				if fmt.Sprintf("%d", p) == page {
					_, _ = fmt.Fprint(w, body)
					return
				}
			}
			_, _ = fmt.Fprint(w, fixtureEmpty)
			return
		}
		http.NotFound(w, r)
	}))
}

func newTestScraper(ts *httptest.Server) *Scraper {
	return &Scraper{
		client: ts.Client(),
		base:   ts.URL,
	}
}

func collect(ch <-chan scraper.SceneResult) []scraper.SceneResult {
	var results []scraper.SceneResult
	for r := range ch {
		results = append(results, r)
	}
	return results
}

func TestListScenes(t *testing.T) {
	ts := newTestServer(map[int]string{1: fixtureListing})
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(ch)

	var scenes int
	for _, r := range results {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}

	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}

	// Find first scene
	for _, r := range results {
		if r.Kind == scraper.KindScene && r.Scene.ID == "10" {
			if r.Scene.Title != "First Scene Title" {
				t.Errorf("title = %q", r.Scene.Title)
			}
			if len(r.Scene.Performers) != 2 {
				t.Errorf("performers = %v", r.Scene.Performers)
			}
			if r.Scene.Studio != "Bare Back Studios" {
				t.Errorf("studio = %q", r.Scene.Studio)
			}
			if len(r.Scene.PriceHistory) != 1 || r.Scene.PriceHistory[0].Regular != 14.99 {
				t.Errorf("price = %v", r.Scene.PriceHistory)
			}
			return
		}
	}
	t.Error("scene ID=10 not found")
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := newTestServer(map[int]string{1: fixtureListing})
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"9": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(ch)
	var scenes, stopped int
	for _, r := range results {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stopped++
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://barebackstudios.com/en/", true},
		{"https://www.barebackstudios.com", true},
		{"https://barebackstudios.com", true},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

// The listing carries title and description in HTML attributes, where an
// ampersand is necessarily written `&amp;` — stored raw, every such title kept
// the entity.
func TestListingTextIsUnescaped(t *testing.T) {
	page := `<html><body>
<div class="video_1 col-lg-3">
    <div class="thumbnail rounded"
        data-video-id="42"
        data-title="Tom &amp; Jerry&#39;s Day"
        data-description="A &quot;quoted&quot; tale &amp; more"
        data-thumb="https://cdn.example.com/t.png">
        <div class="gif-container"><div class="aspect-ratio-box"><img src="p.png" /></div></div>
        <div class="p-2 video-info"><div><small>$ 9.99</small></div></div>
    </div>
</div>
</div>
</body></html>`
	entries := parseListing(page)
	if len(entries) == 0 {
		t.Fatal("no entries parsed")
	}
	if entries[0].title != "Tom & Jerry's Day" {
		t.Errorf("title = %q", entries[0].title)
	}
	if entries[0].description != `A "quoted" tale & more` {
		t.Errorf("description = %q", entries[0].description)
	}
}
