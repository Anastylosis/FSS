package expliciteart

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func cardHTML(id, slug, title, thumb string) string {
	return fmt.Sprintf(`<div class="content">
	<a href="https://www.explicite-art.com/visitor/video/%s-%s.html"><img class="img" src="%s" alt="%s"><div class="typeoverlayer"><img id="tmb_%s" src="/visitor/tpl/main/images/bandeau-video.png"></div></a>
	<div class="vtitle">Paloma %s</div>
</div>`, slug, id, thumb, title, id, title)
}

func detailPage(added, runtime string) string {
	return `<html><head><title>Anal video with a French redhead babe</title></head><body>
<div class="player-info-desc"><p> The full length movie. Featuring Paloma &amp; friends</p> </div>
<script type="text/javascript">
jwplayer("player").setup({
	file: "/medias/sets/trailers/3049.mp4",
	image: "https://www.explicite-art.com/medias/sets/player_thumbs/big.jpg",
	primary: "html5"
});
</script>
<div class="player-info-left">
	<div class="player-info-row">
		<span class="name">RUNTIME</span> ` + runtime + `
		<span class="name">VIEWS</span> 18476
		<span class="name">ADDED</span>  ` + added + `
	</div>
	<div class="player-info-row">
		<span class="name">CATEGORIES</span>
		<span class="tags"><a href="/visitor/channel/16/orgy/" class="link12">Orgy</a></span>
	</div>
	<div class="player-info-row">
		<span class="name">TAGS</span>
		<span class="tags"><a href="/visitor/search/movie/page1.html">movie</a>
		<a href="/visitor/search/film/page1.html">film</a></span>
	</div>
	<div class="player-info-row">
		<span class="name">PORN ACTRESS IN THE VIDEO</span><br>
		<a href="/visitor/pornstars/paloma-403.html">Paloma</a><br />
	</div>
</div>
</body></html>`
}

func newTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/visitor/video/"):
			_, _ = fmt.Fprint(w, detailPage("15 days ago", "10:44"))
		case r.URL.Path == "/visitor/videos/page1.html":
			_, _ = fmt.Fprint(w,
				cardHTML("3049", "principe", "Principe de plaisir", "/medias/sets/thumbs/a.jpg")+
					cardHTML("2718", "arabic-teen", "Arabic teen &amp; friends", "https://cdn.example.com/b.jpg"))
		case r.URL.Path == "/visitor/videos/page2.html":
			_, _ = fmt.Fprint(w, cardHTML("2740", "threesome", "Threesome", "/medias/sets/thumbs/c.jpg"))
		case r.URL.Path == "/visitor/pornstars/paloma-403.html":
			_, _ = fmt.Fprint(w, cardHTML("3049", "principe", "Principe de plaisir", "/medias/sets/thumbs/a.jpg"))
		default:
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
		}
	}))
}

func newTestScraper(ts *httptest.Server) *Scraper {
	return &Scraper{client: ts.Client(), base: ts.URL}
}

func collect(ch <-chan scraper.SceneResult) (scenes []models.Scene, errs, stopped int) {
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs++
		case scraper.KindStoppedEarly:
			stopped++
		}
	}
	return scenes, errs, stopped
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.explicite-art.com/", true},
		{"https://explicite-art.com/visitor/", true},
		{"http://www.explicite-art.com/visitor/channel/16/orgy/", true},
		{"https://explicite-art.com.evil.net/", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestResolveListing(t *testing.T) {
	tests := []struct {
		url    string
		prefix string
		single bool
	}{
		{"https://www.explicite-art.com/", "/visitor/videos/", false},
		{"https://www.explicite-art.com/visitor/", "/visitor/videos/", false},
		{"https://www.explicite-art.com/visitor/videos/page3.html", "/visitor/videos/", false},
		{"https://www.explicite-art.com/visitor/channel/16/orgy/", "/visitor/channel/16/orgy/", false},
		{"https://www.explicite-art.com/visitor/channel/16/orgy/page2.html", "/visitor/channel/16/orgy/", false},
		{"https://www.explicite-art.com/visitor/search/movie/page1.html", "/visitor/search/movie/", false},
		{"https://www.explicite-art.com/visitor/pornstars/best-of-53.html", "/visitor/pornstars/best-of-53.html", true},
	}
	for _, tt := range tests {
		got := resolveListing(tt.url)
		if got.prefix != tt.prefix || got.single != tt.single {
			t.Errorf("resolveListing(%q) = %+v, want prefix %q single %v", tt.url, got, tt.prefix, tt.single)
		}
	}
}

func TestParseListing(t *testing.T) {
	page := cardHTML("3049", "principe", "Principe de plaisir", "/medias/sets/thumbs/a.jpg") +
		cardHTML("2718", "arabic-teen", "Arabic teen &amp; friends", "https://cdn.example.com/b.jpg")
	cards := parseListing(page)
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	if cards[0].id != "3049" || cards[0].path != "/visitor/video/principe-3049.html" {
		t.Errorf("card = %+v", cards[0])
	}
	// The card's alt is the clean title; the neighbouring vtitle prepends the
	// performer's name.
	if cards[0].title != "Principe de plaisir" {
		t.Errorf("title = %q", cards[0].title)
	}
	if cards[1].title != "Arabic teen & friends" {
		t.Errorf("title = %q", cards[1].title)
	}
}

func TestParseRuntime(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"1:38:08", 5888},
		{"10:44", 644},
		{"89:15", 5355},
		// A bare value is a count of minutes.
		{"102", 6120},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseRuntime(tt.in); got != tt.want {
			t.Errorf("parseRuntime(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// The tour prints "365 days ago" for everything older than a year, so that
// value is a cap rather than a date.
func TestParseAdded(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		added string
		want  string
	}{
		{"15 days ago", "2026-09-02"},
		{"64 days ago", "2026-07-15"},
		{"3 hours ago", "2026-09-17"},
		{"2 months ago", "2026-07-19"},
		{"365 days ago", ""},
		{"400 days ago", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got, ok := parseAdded(`<span class="name">ADDED</span>  `+tt.added, now)
		if tt.want == "" {
			if ok {
				t.Errorf("parseAdded(%q) = %v, want none", tt.added, got)
			}
			continue
		}
		if !ok || got.Format("2006-01-02") != tt.want {
			t.Errorf("parseAdded(%q) = %v/%v, want %s", tt.added, got, ok, tt.want)
		}
	}
}

func TestApplyDetail(t *testing.T) {
	var sc models.Scene
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	applyDetail(&sc, detailPage("15 days ago", "10:44"), "https://www.explicite-art.com", now)

	if sc.Title != "Anal video with a French redhead babe" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.Description != "The full length movie. Featuring Paloma & friends" {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.Duration != 644 {
		t.Errorf("duration = %d", sc.Duration)
	}
	if sc.Views != 18476 {
		t.Errorf("views = %d", sc.Views)
	}
	if len(sc.Categories) != 1 || sc.Categories[0] != "Orgy" {
		t.Errorf("categories = %v", sc.Categories)
	}
	if len(sc.Tags) != 2 || sc.Tags[0] != "movie" {
		t.Errorf("tags = %v", sc.Tags)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Paloma" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if sc.Date.Format("2006-01-02") != "2026-09-02" {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Preview != "https://www.explicite-art.com/medias/sets/trailers/3049.mp4" {
		t.Errorf("preview = %q", sc.Preview)
	}
	if sc.Thumbnail != "https://www.explicite-art.com/medias/sets/player_thumbs/big.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/visitor/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collect(ch)
	if errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	if !strings.HasPrefix(scenes[0].URL, ts.URL) {
		t.Errorf("URL = %q, want the test server host", scenes[0].URL)
	}
	if scenes[0].Studio != studioName || scenes[0].SiteID != siteID {
		t.Errorf("scene = %+v", scenes[0])
	}
}

// A performer's page does not paginate, so the walk is one request.
func TestListScenesPornstarIsASinglePage(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/visitor/video/") {
			_, _ = fmt.Fprint(w, detailPage("15 days ago", "10:44"))
			return
		}
		calls++
		_, _ = fmt.Fprint(w, cardHTML("3049", "principe", "Principe", "/a.jpg"))
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/visitor/pornstars/paloma-403.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, _ := collect(ch)
	if len(scenes) != 1 {
		t.Errorf("got %d scenes, want 1", len(scenes))
	}
	if calls != 1 {
		t.Errorf("fetched %d listing pages, want 1", calls)
	}
}

// The tour orders by neither date nor id, so the early-stop hint is dropped
// rather than truncating the walk at an arbitrary point.
func TestKnownIDsDoNotStopTheWalk(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/visitor/", scraper.ListOpts{
		KnownIDs: map[string]bool{"3049": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, stopped := collect(ch)
	if len(scenes) != 3 {
		t.Errorf("got %d scenes, want all 3", len(scenes))
	}
	if stopped != 0 {
		t.Errorf("got %d stoppedEarly, want 0", stopped)
	}
}

// A first page that loads but yields no cards is a parser failure, not an
// empty catalogue.
func TestEmptyFirstPageIsAParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body></body></html>`)
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/visitor/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
			if got := scraper.Classify(r.Err); got != scraper.FailureParse {
				t.Errorf("Classify = %v, want FailureParse", got)
			}
		}
	}
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}
