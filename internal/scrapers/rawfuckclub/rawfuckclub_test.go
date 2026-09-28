package rawfuckclub

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

const fixtureListing = `<html><body data-siteid="RFC">
<div class="row jscroll-inner browse_entries">
<div class="col-lg-3 col-sm-4 col-6 browse-last-update-item last-update-item p-0 watch-slide watch-slide-new" data-position="1" data-page="1"><div class="last-update-item-inner"><div class="position-relative videoPreviewDemo" data-src="https://cdn.example.com/AB12/still_large.jpg" data-preview="https://previews.example.com/AB12.mp4" data-href="/video/AB12-test-first-scene"><a href="/video/AB12-test-first-scene" class="stateful-link" data-state="watch" title="First Scene Title"><img class="img-responsive" src="/images/transparent_video.png" alt="First Scene Title" /></a><div class="camera-like-date">Posted 1D ago</div></div></div><div class="last-update-title"><a href="/video/AB12-test-first-scene" title="First Scene Title">First Scene Title</a></div><div class="browse-channel-name"><a href="/testchannel" data-toggle="tooltip" title="Test Channel">Test Channel</a></div></div>
<div class="col-lg-3 col-sm-4 col-6 browse-last-update-item last-update-item p-0 watch-slide watch-slide-new" data-position="2" data-page="1"><div class="last-update-item-inner"><div class="position-relative videoPreviewDemo" data-src="https://cdn.example.com/CD34/still_large.jpg" data-preview="" data-href="/video/CD34-other-second-scene"><a href="/video/CD34-other-second-scene" class="stateful-link" data-state="watch" title="Second Scene"><img class="img-responsive" src="/images/transparent_video.png" alt="Second Scene" /></a><div class="camera-like-date">Posted 2W ago</div></div></div><div class="last-update-title"><a href="/video/CD34-other-second-scene" title="Second Scene">Second Scene</a></div><div class="browse-channel-name"><a href="/otherchannel" data-toggle="tooltip" title="Other Channel">Other Channel</a></div></div>
</div>
</body></html>`

const fixtureDetail = `<html><body>
<h2 style="margin-top: 15px; font-size: 2.2em;">First Scene Title</h2>
<div class="watch-duration d-inline-block">22 minutes</div>
<div class="watch-channel-name" style="padding: 0; margin: 0;"><a href="/testchannel">Test Channel</a></div>
<p class="watch-published-date" style="padding: 0; margin: 0">Posted on May 7, 2026</p>
<p class="watch-description">A great scene description</p>
<div class="watch-badges"><div class="tag-badges">
<a href="https://www.rawfuckclub.com/performer1"><span class="badge badge-primary">Performer One</span></a>
<a href="https://www.rawfuckclub.com/performer2"><span class="badge badge-primary">Performer Two</span></a>
<a href="https://www.rawfuckclub.com/tagone"><span class="badge badge-secondary">Tag One</span></a>
<a href="https://www.rawfuckclub.com/tagtwo"><span class="badge badge-secondary">Tag Two</span></a>
</div></div>
<button type="button">Buy $9.95</button>
</body></html>`

const fixtureDetailReposted = `<html><body>
<h2>Reposted Scene</h2>
<div class="watch-duration d-inline-block">30 minutes</div>
<div class="watch-channel-name"><a href="/channel">Some Channel</a></div>
<p class="watch-published-date">Reposted on May 7, 2026.&nbsp;<a href="javascript:void(0);" class="showtitle" title="Originally posted  on April 1, 2026"><svg></svg></a></p>
<div class="watch-badges"><div class="tag-badges">
<a href="https://www.rawfuckclub.com/actor"><span class="badge badge-primary">Actor Name</span></a>
<a href="https://www.rawfuckclub.com/tag"><span class="badge badge-secondary">Some Tag</span></a>
</div></div>
</body></html>`

const fixtureEmpty = `<html><body></body></html>`

func TestParseListing(t *testing.T) {
	entries := parseListing(fixtureListing)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	e := entries[0]
	if e.id != "AB12" {
		t.Errorf("id = %q, want AB12", e.id)
	}
	if e.title != "First Scene Title" {
		t.Errorf("title = %q", e.title)
	}
	if e.url != "/video/AB12-test-first-scene" {
		t.Errorf("url = %q", e.url)
	}
	if e.thumb != "https://cdn.example.com/AB12/still_large.jpg" {
		t.Errorf("thumb = %q", e.thumb)
	}
	if e.channel != "Test Channel" {
		t.Errorf("channel = %q", e.channel)
	}

	e2 := entries[1]
	if e2.id != "CD34" {
		t.Errorf("id = %q, want CD34", e2.id)
	}
	if e2.channel != "Other Channel" {
		t.Errorf("channel = %q", e2.channel)
	}
}

func TestParseDetail(t *testing.T) {
	d := parseDetail(fixtureDetail)
	if len(d.performers) != 2 || d.performers[0] != "Performer One" || d.performers[1] != "Performer Two" {
		t.Errorf("performers = %v", d.performers)
	}
	if d.date != "May 7, 2026" {
		t.Errorf("date = %q, want May 7, 2026", d.date)
	}
	if d.duration != 1320 {
		t.Errorf("duration = %d, want 1320 (22 min)", d.duration)
	}
	if d.studio != "Test Channel" {
		t.Errorf("studio = %q", d.studio)
	}
	if len(d.tags) != 2 || d.tags[0] != "Tag One" || d.tags[1] != "Tag Two" {
		t.Errorf("tags = %v", d.tags)
	}
	if d.description != "A great scene description" {
		t.Errorf("description = %q", d.description)
	}
	if d.buyPrice != 9.95 {
		t.Errorf("buyPrice = %f, want 9.95", d.buyPrice)
	}
}

func TestParseDetailReposted(t *testing.T) {
	d := parseDetail(fixtureDetailReposted)
	if d.date != "April 1, 2026" {
		t.Errorf("date = %q, want April 1, 2026 (original date)", d.date)
	}
	if d.duration != 1800 {
		t.Errorf("duration = %d, want 1800 (30 min)", d.duration)
	}
	if d.studio != "Some Channel" {
		t.Errorf("studio = %q", d.studio)
	}
	if len(d.performers) != 1 || d.performers[0] != "Actor Name" {
		t.Errorf("performers = %v", d.performers)
	}
}

func TestResolveMode(t *testing.T) {
	s := New()
	tests := []struct{ url, wantPath string }{
		{"https://www.rawfuckclub.com", "/browse/new"},
		{"https://www.rawfuckclub.com/browse/new", "/browse/new"},
		{"https://www.rawfuckclub.com/RawFuckClub", "/RawFuckClub/newest_uploads"},
		{"https://www.rawfuckclub.com/SomeChannel/newest_uploads", "/SomeChannel/newest_uploads"},
		{"https://www.rawfuckclub.com/browse/trending", "/browse/trending"},
	}
	for _, tt := range tests {
		if path := s.resolveMode(tt.url); path != tt.wantPath {
			t.Errorf("resolveMode(%q) path = %q, want %q", tt.url, path, tt.wantPath)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.rawfuckclub.com/browse/new", true},
		{"https://rawfuckclub.com/SomeChannel", true},
		{"https://www.rawfuckclub.com", true},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func newTestServer(listingPages map[int]string, details map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/browse/new":
			page := 1
			if p := r.URL.Query().Get("page"); p != "" {
				var pn int
				if _, err := fmt.Sscanf(p, "%d", &pn); err == nil {
					page = pn
				}
			}
			if body, ok := listingPages[page]; ok {
				_, _ = fmt.Fprint(w, body)
				return
			}
		default:
			if body, ok := details[r.URL.Path]; ok {
				_, _ = fmt.Fprint(w, body)
				return
			}
		}
		_, _ = fmt.Fprint(w, fixtureEmpty)
	}))
}

func collect(ch <-chan scraper.SceneResult) []scraper.SceneResult {
	var results []scraper.SceneResult
	for r := range ch {
		results = append(results, r)
	}
	return results
}

func TestListScenes(t *testing.T) {
	ts := newTestServer(
		map[int]string{1: fixtureListing},
		map[string]string{
			"/video/AB12-test-first-scene":   fixtureDetail,
			"/video/CD34-other-second-scene": fixtureDetail,
		},
	)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/browse/new", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(ch)
	var scenes int
	for _, r := range results {
		if r.Kind == scraper.KindScene {
			scenes++
			if r.Scene.ID == "AB12" {
				if len(r.Scene.Performers) != 2 {
					t.Errorf("performers = %v", r.Scene.Performers)
				}
				if r.Scene.Studio != "Test Channel" {
					t.Errorf("studio = %q", r.Scene.Studio)
				}
				if r.Scene.Duration != 1320 {
					t.Errorf("duration = %d, want 1320", r.Scene.Duration)
				}
			}
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := newTestServer(
		map[int]string{1: fixtureListing},
		map[string]string{
			"/video/AB12-test-first-scene": fixtureDetail,
		},
	)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/browse/new", scraper.ListOpts{
		KnownIDs: map[string]bool{"CD34": true},
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

// /browse/new is ordered by upload date and a repost re-uploads, so a stored
// scene routinely sits at the top of page one — live, eight of the first ten
// cards were reposts of 2025 videos. A stop there would end the run before
// reaching anything new, so the known card must only be skipped.
func TestKnownIDAtTheTopDoesNotHideTheRest(t *testing.T) {
	ts := newTestServer(
		map[int]string{1: fixtureListing},
		map[string]string{
			"/video/CD34-other-second-scene": fixtureDetail,
		},
	)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/browse/new", scraper.ListOpts{
		KnownIDs: map[string]bool{"AB12": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var ids []string
	var stopped int
	for _, r := range collect(ch) {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindStoppedEarly:
			stopped++
		}
	}
	if len(ids) != 1 || ids[0] != "CD34" {
		t.Errorf("scene IDs = %v, want [CD34]", ids)
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
}
