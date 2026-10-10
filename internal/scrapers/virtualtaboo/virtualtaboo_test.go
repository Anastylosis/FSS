package virtualtaboo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteCount(t *testing.T) {
	if len(sites) != 3 {
		t.Errorf("expected 3 sites, got %d", len(sites))
	}
}

func TestNoDuplicateSiteIDs(t *testing.T) {
	seen := make(map[string]bool)
	for _, cfg := range sites {
		if seen[cfg.siteID] {
			t.Errorf("duplicate siteID: %s", cfg.siteID)
		}
		seen[cfg.siteID] = true
	}
}

func TestMatchesURL(t *testing.T) {
	s := &Scraper{
		cfg:     sites[2],
		matchRe: newMatchRe(sites[2].domain),
	}
	tests := []struct {
		url  string
		want bool
	}{
		{"https://virtualtaboo.com/videos", true},
		{"https://www.virtualtaboo.com/videos", true},
		{"http://virtualtaboo.com/videos/some-scene", true},
		{"https://darkroomvr.com/video", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func newMatchRe(domain string) *regexp.Regexp {
	escaped := strings.ReplaceAll(domain, ".", `\.`)
	return regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?%s`, escaped))
}

const listingHTML = `<html><body>
<div class="video-card__item video">
  <a class="image-container" href="/video/hot-vr-scene">
    <img src="https://static.example.com/thumbs/hot-vr-scene.jpg" />
  </a>
  <div class="video-card__title">Hot VR Scene</div>
  <div class="video-card__actors">
    <a href="/pornstars/jane-doe">Jane Doe</a>,
    <a href="/pornstars/john-smith">John Smith</a>
  </div>
</div>
<div class="video-card__item video">
  <a class="image-container" href="/video/another-scene">
    <img src="https://static.example.com/thumbs/another-scene.jpg" />
  </a>
  <div class="video-card__title">Another Scene</div>
  <div class="video-card__actors">
    <a href="/model/alice">Alice</a>
  </div>
</div>
<a href="/videos?page=1">1</a>
<a href="/videos?page=2">2</a>
<a href="/videos?page=3">3</a>
</body></html>`

func TestParseListingEntries(t *testing.T) {
	s := &Scraper{cfg: sites[2], baseOverride: "https://virtualtaboo.com"}
	entries := s.parseListingEntries([]byte(listingHTML))
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	e := entries[0]
	if e.slug != "hot-vr-scene" {
		t.Errorf("slug = %q", e.slug)
	}
	if e.title != "Hot VR Scene" {
		t.Errorf("title = %q", e.title)
	}
	if len(e.performers) != 2 || e.performers[0] != "Jane Doe" || e.performers[1] != "John Smith" {
		t.Errorf("performers = %v", e.performers)
	}
	if e.thumbnail != "https://static.example.com/thumbs/hot-vr-scene.jpg" {
		t.Errorf("thumbnail = %q", e.thumbnail)
	}
	if !strings.HasPrefix(e.url, "https://virtualtaboo.com/video/hot-vr-scene") {
		t.Errorf("url = %q", e.url)
	}

	e2 := entries[1]
	if e2.slug != "another-scene" {
		t.Errorf("entry 2 slug = %q", e2.slug)
	}
	if len(e2.performers) != 1 || e2.performers[0] != "Alice" {
		t.Errorf("entry 2 performers = %v", e2.performers)
	}
}

func TestParseMaxPage(t *testing.T) {
	if got := parseMaxPage([]byte(listingHTML)); got != 3 {
		t.Errorf("parseMaxPage = %d, want 3", got)
	}
}

func TestHasNextPage(t *testing.T) {
	if !hasNextPage([]byte(listingHTML), 1) {
		t.Error("expected hasNextPage(1) = true")
	}
	if hasNextPage([]byte(listingHTML), 3) {
		t.Error("expected hasNextPage(3) = false")
	}
}

func TestExtractSlug(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"/video/hot-scene", "hot-scene"},
		{"https://virtualtaboo.com/video/hot-scene", "hot-scene"},
		{"/video/hot-scene?trailer=1", "hot-scene"},
		{"/model/jane-doe", ""},
		{"/videos", ""},
		{"/video/best", "best"},
	}
	for _, tt := range tests {
		if got := extractSlug(tt.url); got != tt.want {
			t.Errorf("extractSlug(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

const detailHTML = `<html><head>
<script type="application/ld+json">
{
  "@type": "VideoObject",
  "name": "Hot VR Scene",
  "description": "A great VR experience with Jane Doe and John Smith.",
  "uploadDate": "2025-11-15",
  "duration": "T0H32M15S",
  "thumbnailUrl": "https://static.example.com/thumbs/hot-vr-scene-detail.jpg"
}
</script>
</head><body>
<a href="/tag/vr">VR</a>
<a href="/tag/blonde">Blonde</a>
<a href="/model/jane-doe">Jane Doe</a>
<a href="/model/john-smith">John Smith</a>
</body></html>`

func TestParseDetail(t *testing.T) {
	s := &Scraper{cfg: sites[2], baseOverride: "https://virtualtaboo.com"}
	entry := listEntry{
		slug:       "hot-vr-scene",
		url:        "https://virtualtaboo.com/video/hot-vr-scene",
		title:      "Hot VR Scene",
		performers: []string{"Jane Doe", "John Smith"},
		thumbnail:  "https://static.example.com/thumbs/hot-vr-scene.jpg",
	}

	scene := s.parseDetail([]byte(detailHTML), entry, testStudioURL)
	if scene.StudioURL != testStudioURL {
		t.Errorf("StudioURL = %q, want %q", scene.StudioURL, testStudioURL)
	}

	if scene.ID != "hot-vr-scene" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "virtualtaboo" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Title != "Hot VR Scene" {
		t.Errorf("title = %q", scene.Title)
	}
	if scene.Description != "A great VR experience with Jane Doe and John Smith." {
		t.Errorf("description = %q", scene.Description)
	}
	if scene.Date.Format("2006-01-02") != "2025-11-15" {
		t.Errorf("date = %v", scene.Date)
	}
	if scene.Duration != 32*60+15 {
		t.Errorf("duration = %d, want %d", scene.Duration, 32*60+15)
	}
	if scene.Thumbnail != "https://static.example.com/thumbs/hot-vr-scene.jpg" {
		t.Errorf("thumbnail = %q (should keep listing thumbnail)", scene.Thumbnail)
	}
	if len(scene.Tags) != 2 || scene.Tags[0] != "VR" || scene.Tags[1] != "Blonde" {
		t.Errorf("tags = %v", scene.Tags)
	}
	if scene.Studio != "Virtual Taboo" {
		t.Errorf("studio = %q", scene.Studio)
	}
}

func TestParseDetailFallbackTitle(t *testing.T) {
	s := &Scraper{cfg: sites[0], baseOverride: "https://darkroomvr.com"}
	entry := listEntry{
		slug: "some-scene",
		url:  "https://darkroomvr.com/video/some-scene",
	}

	scene := s.parseDetail([]byte(detailHTML), entry, testStudioURL)

	if scene.Title != "Hot VR Scene" {
		t.Errorf("expected JSON-LD title fallback, got %q", scene.Title)
	}
	if scene.SiteID != "darkroomvr" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Studio != "Dark Room VR" {
		t.Errorf("studio = %q", scene.Studio)
	}
}

func TestParseDetailFallbackPerformers(t *testing.T) {
	s := &Scraper{cfg: sites[2], baseOverride: "https://virtualtaboo.com"}
	entry := listEntry{
		slug: "some-scene",
		url:  "https://virtualtaboo.com/video/some-scene",
	}

	scene := s.parseDetail([]byte(detailHTML), entry, testStudioURL)

	if len(scene.Performers) != 2 || scene.Performers[0] != "Jane Doe" {
		t.Errorf("expected performers from model links, got %v", scene.Performers)
	}
}

func TestParseDetailFallbackThumbnail(t *testing.T) {
	s := &Scraper{cfg: sites[2], baseOverride: "https://virtualtaboo.com"}
	entry := listEntry{
		slug: "some-scene",
		url:  "https://virtualtaboo.com/video/some-scene",
	}

	scene := s.parseDetail([]byte(detailHTML), entry, testStudioURL)

	if scene.Thumbnail != "https://static.example.com/thumbs/hot-vr-scene-detail.jpg" {
		t.Errorf("expected JSON-LD thumbnail fallback, got %q", scene.Thumbnail)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{`"duration": "T2H30M15S"`, 2*3600 + 30*60 + 15},
		{`"duration": "T30M15S"`, 30*60 + 15},
		{`"duration": "T45M"`, 45 * 60},
	}
	for _, tt := range tests {
		m := jldDurRe.FindStringSubmatch(tt.input)
		if m == nil {
			t.Fatalf("no match for %q", tt.input)
		}
		if got := parseDuration(m); got != tt.want {
			t.Errorf("parseDuration(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		page := r.URL.Query().Get("page")
		switch {
		case r.URL.Path == "/videos" && (page == "1" || page == ""):
			_, _ = fmt.Fprint(w, listingHTML)
		case r.URL.Path == "/videos" && page == "2":
			_, _ = fmt.Fprint(w, `<html><body>
<div class="video-card__item video">
  <a class="image-container" href="/video/third-scene">
    <img src="https://static.example.com/thumbs/third.jpg" />
  </a>
  <div class="video-card__title">Third Scene</div>
</div>
</body></html>`)
		case r.URL.Path == "/videos" && page == "3":
			_, _ = fmt.Fprint(w, "<html></html>")
		case strings.HasPrefix(r.URL.Path, "/video/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	s := &Scraper{
		cfg:          sites[2],
		client:       ts.Client(),
		baseOverride: ts.URL,
		matchRe:      newMatchRe(sites[2].domain),
	}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if want := ts.URL + "/videos"; r.Scene.StudioURL != want {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, want)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 3 {
		t.Errorf("got %d scenes, want 3", scenes)
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch {
		case strings.Contains(r.URL.RawQuery, "page="):
			_, _ = fmt.Fprint(w, listingHTML)
		case strings.HasPrefix(r.URL.Path, "/video/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	s := &Scraper{
		cfg:          sites[2],
		client:       ts.Client(),
		baseOverride: ts.URL,
		matchRe:      newMatchRe(sites[2].domain),
	}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"another-scene": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	var stoppedEarly bool
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
}

func TestModelPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch {
		case strings.HasPrefix(r.URL.Path, "/pornstars/"), strings.HasPrefix(r.URL.Path, "/model/"):
			_, _ = fmt.Fprint(w, listingHTML)
		case strings.HasPrefix(r.URL.Path, "/video/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	// The live site uses /pornstars/{slug}; /model/ is kept for backward compat.
	for _, performerURL := range []string{"/pornstars/jane-doe", "/model/jane-doe"} {
		s := &Scraper{
			cfg:          sites[2],
			client:       ts.Client(),
			baseOverride: ts.URL,
			matchRe:      newMatchRe(sites[2].domain),
		}

		ch, err := s.ListScenes(context.Background(), ts.URL+performerURL, scraper.ListOpts{Workers: 1})
		if err != nil {
			t.Fatal(err)
		}

		var scenes int
		for r := range ch {
			switch r.Kind {
			case scraper.KindScene:
				scenes++
				if want := ts.URL + performerURL; r.Scene.StudioURL != want {
					t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, want)
				}
			case scraper.KindError:
				t.Errorf("unexpected error: %v", r.Err)
			}
		}
		if scenes != 2 {
			t.Errorf("%s: got %d scenes, want 2", performerURL, scenes)
		}
	}
}

const testStudioURL = "https://virtualtaboo.com/videos"
