package oopsfamily

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://oopsfamily.com/", true},
		{"https://oopsfamily.com", true},
		{"https://www.oopsfamily.com/video", true},
		{"https://oopsfamily.com/model/sophie-locke", true},
		{"https://oopsfamily.com/tag/redhead", true},
		{"https://example.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestExtractID(t *testing.T) {
	cases := []struct {
		url, want string
	}{
		{"https://oopsfamily.com/video/cant-stop-watching-this-offf", "offf"},
		{"https://oopsfamily.com/video/the-teacher-crush-off7", "off7"},
		{"https://oopsfamily.com/video/lap-of-luxury-5448257", "5448257"},
		{"https://oopsfamily.com/video", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := extractID(c.url); got != c.want {
			t.Errorf("extractID(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestResolveListingBase(t *testing.T) {
	s := New()
	cases := []struct {
		url, want string
	}{
		{"https://oopsfamily.com/", siteBase + "/video"},
		{"https://oopsfamily.com", siteBase + "/video"},
		{"https://oopsfamily.com/model/sophie-locke", siteBase + "/model/sophie-locke"},
		{"https://oopsfamily.com/tag/redhead", siteBase + "/tag/redhead"},
	}
	for _, c := range cases {
		if got := s.resolveListingBase(c.url); got != c.want {
			t.Errorf("resolveListingBase(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

const fixtureCard = `
<div class="video-card__item"
     >
    <a class="image-container" href="https://oopsfamily.com/video/cant-stop-watching-this-offf">
        <img src="https://static.oopsfamily.com/poster/148/1900/1.jpg?v=123"
             alt="Can't Stop Watching This">
        <div class="video-card__quality">
            <img src="https://static.oopsfamily.com/img/icons/icon-4K.svg" alt="4K"> 30:55
        </div>
    </a>
    <div class="video-card__description">
        <a href="https://oopsfamily.com/video/cant-stop-watching-this-offf" class="video-card__title">
            Can't Stop Watching This
        </a>
        <div class="video-card__actors mr-4">
            <a href="https://oopsfamily.com/model/lily-lou">
                Lily Lou
            </a>
            <span>, </span>
            <a href="https://oopsfamily.com/model/sage-roux">
                Sage Roux
            </a>
        </div>
        <div class="video-card__icons">
`

func TestParseListingCards(t *testing.T) {
	cards, hasNext := parseListingPage([]byte(fixtureCard))
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}
	c := cards[0]
	if c.id != "offf" {
		t.Errorf("id = %q", c.id)
	}
	if c.url != "https://oopsfamily.com/video/cant-stop-watching-this-offf" {
		t.Errorf("url = %q", c.url)
	}
	if c.title != "Can't Stop Watching This" {
		t.Errorf("title = %q", c.title)
	}
	if c.thumbnail != "https://static.oopsfamily.com/poster/148/1900/1.jpg?v=123" {
		t.Errorf("thumbnail = %q", c.thumbnail)
	}
	if c.duration != 30*60+55 {
		t.Errorf("duration = %d, want %d", c.duration, 30*60+55)
	}
	if len(c.performers) != 2 || c.performers[0] != "Lily Lou" || c.performers[1] != "Sage Roux" {
		t.Errorf("performers = %v", c.performers)
	}
	if hasNext {
		t.Error("hasNext should be false (no pagination__next link)")
	}
}

func TestParseListingCardsWithPagination(t *testing.T) {
	html := fixtureCard + `<a href="?page=2" class="pagination__next icon-right-arr">`
	_, hasNext := parseListingPage([]byte(html))
	if !hasNext {
		t.Error("hasNext should be true")
	}
}

func TestParseDetailPage(t *testing.T) {
	html := `<html><script type="application/ld+json">
{"@type":"VideoObject","uploadDate":"2026-04-24T07:20:12+00:00","genre":["Pornography","Handjob","Babe","Young"],"actor":[{"name":"Lily Lou"}]}
</script></html>`

	d := parseDetailPage([]byte(html))
	if d.date.Year() != 2026 || d.date.Month() != 4 || d.date.Day() != 24 {
		t.Errorf("date = %v", d.date)
	}
	if len(d.tags) != 3 || d.tags[0] != "Handjob" {
		t.Errorf("tags = %v (should exclude Pornography)", d.tags)
	}
}

func TestBuildScene(t *testing.T) {
	c := listingCard{
		id:         "offf",
		url:        "https://oopsfamily.com/video/cant-stop-watching-this-offf",
		title:      "Can't Stop Watching This",
		thumbnail:  "https://static.oopsfamily.com/poster/148/1900/1.jpg",
		duration:   1855,
		performers: []string{"Lily Lou", "Sage Roux"},
	}
	d := detailData{
		date: time.Date(2026, 4, 24, 7, 20, 12, 0, time.UTC),
		tags: []string{"Handjob", "Babe"},
	}
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	scene := buildScene("https://oopsfamily.com/", c, d, now)

	if scene.ID != "offf" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.Title != "Can't Stop Watching This" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Duration != 1855 {
		t.Errorf("Duration = %d", scene.Duration)
	}
	if len(scene.Tags) != 2 {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Width != 3840 || scene.Resolution != "2160p" {
		t.Errorf("Width=%d Resolution=%q", scene.Width, scene.Resolution)
	}
	if scene.Studio != "OopsFamily" {
		t.Errorf("Studio = %q", scene.Studio)
	}
}

func cardFixture(base string, id int) string {
	return fmt.Sprintf(`
<div class="video-card__item">
    <a class="image-container" href="%s/video/scene-%d-s%d">
        <img src="%s/thumb/%d.jpg" alt="Scene %d">
        <div class="video-card__quality">
            <img src="/img/icons/icon-4K.svg" alt="4K"> %d:30
        </div>
    </a>
    <div class="video-card__description">
        <a href="%s/video/scene-%d-s%d" class="video-card__title">
            Scene %d
        </a>
        <div class="video-card__actors mr-4">
            <a href="%s/model/performer-%d">Performer %d</a>
        </div>
        <div class="video-card__icons">`,
		base, id, id, base, id, id, id, base, id, id, id, base, id, id)
}

func detailFixture(id int) string {
	return fmt.Sprintf(`<html><script type="application/ld+json">
{"@type":"VideoObject","uploadDate":"2026-01-%02dT10:00:00+00:00","genre":["Pornography","Tag%d"]}
</script></html>`, id, id)
}

func newTestServer(base *string, sceneCount int) *httptest.Server {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/video", "/model/someone", "/tag/redhead":
			page := r.URL.Query().Get("page")
			if page == "2" || page == "" && sceneCount == 0 {
				_, _ = fmt.Fprint(w, `<html></html>`)
				return
			}
			var html string
			for i := 1; i <= sceneCount; i++ {
				html += cardFixture(ts.URL, i)
			}
			if sceneCount > 0 {
				html += `<a href="?page=2" class="pagination__next icon-right-arr">`
			}
			_, _ = fmt.Fprint(w, html)
		default:
			var id int
			_, _ = fmt.Sscanf(r.URL.Path, "/video/scene-%d-", &id)
			if id > 0 {
				_, _ = fmt.Fprint(w, detailFixture(id))
			} else {
				w.WriteHeader(404)
			}
		}
	}))
	*base = ts.URL
	return ts
}

func TestListScenes(t *testing.T) {
	var base string
	ts := newTestServer(&base, 3)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: base}

	ch, err := s.ListScenes(context.Background(), base, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 3 {
		t.Fatalf("got %d scenes, want 3", len(results))
	}
	for _, sc := range results {
		if sc.SiteID != "oopsfamily" {
			t.Errorf("siteID = %q", sc.SiteID)
		}
		if sc.Studio != "OopsFamily" {
			t.Errorf("studio = %q", sc.Studio)
		}
		if sc.Title == "" {
			t.Error("title is empty")
		}
		if len(sc.Performers) != 1 {
			t.Errorf("performers = %v", sc.Performers)
		}
		if sc.Duration == 0 {
			t.Error("duration is 0")
		}
		if sc.Date.IsZero() {
			t.Error("date is zero")
		}
		if len(sc.Tags) == 0 {
			t.Error("tags is empty")
		}
		if sc.Width != 3840 || sc.Resolution != "2160p" {
			t.Errorf("resolution: %dx%d %s", sc.Width, sc.Height, sc.Resolution)
		}
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	var base string
	ts := newTestServer(&base, 3)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: base}

	ch, err := s.ListScenes(context.Background(), base, scraper.ListOpts{
		KnownIDs: map[string]bool{"s2": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(results) != 1 {
		t.Fatalf("got %d scenes, want 1 (before known ID)", len(results))
	}
}

func TestListScenesModelPage(t *testing.T) {
	var base string
	ts := newTestServer(&base, 2)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: base}

	ch, err := s.ListScenes(context.Background(), base+"/model/someone", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
}

// An unreleased scene's detail page answers 403, so its date is unknowable.
// It used to fall back to the scrape time, which stored today as the release
// date of a scene that has not been released — wrong data that
// preserveEnrichment cannot undo, since the field is not empty.
func TestListScenesUnreadableDetailLeavesTheDateUnknown(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/video":
			if r.URL.Query().Get("page") == "2" {
				_, _ = fmt.Fprint(w, `<html></html>`)
				return
			}
			_, _ = fmt.Fprint(w, cardFixture(r.Host, 1))
		default:
			_, _ = fmt.Fprint(w, `<html></html>`)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes []models.Scene
	var errs int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs++
		}
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1 — the card still names the scene", len(scenes))
	}
	if !scenes[0].Date.IsZero() {
		t.Errorf("Date = %v, want zero — an unknown date must not be invented", scenes[0].Date)
	}
	if errs == 0 {
		t.Error("the detail failure must be reported, not swallowed")
	}
}
