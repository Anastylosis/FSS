package paysitemanagerutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func testSite() SiteConfig {
	return SiteConfig{SiteID: "thesensitivespot", SiteBase: "https://thesensitivespot.com", StudioName: "The Sensitive Spot"}
}

func TestMatchesURL(t *testing.T) {
	s := New(testSite())
	for _, u := range []string{
		"https://thesensitivespot.com/",
		"https://www.thesensitivespot.com/updates",
		"http://thesensitivespot.com",
		"https://thesensitivespot.com/tags/lesbianarmpit",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://thesensitivespot.net/", "https://notthesensitivespot.com/", "https://example.com/thesensitivespot.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestListPathDefault(t *testing.T) {
	if got := (SiteConfig{}).listPath(); got != "/updates" {
		t.Errorf("listPath() = %q", got)
	}
	if got := (SiteConfig{ListPath: "/scenes"}).listPath(); got != "/scenes" {
		t.Errorf("listPath() = %q", got)
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing_page1.html"))
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	// The scene id is the numeric content id in the thumbnail path, not the
	// slug — the slug is derived from the title.
	if first.id == "" || strings.ContainsAny(first.id, "-/") {
		t.Errorf("id = %q, want a numeric content id", first.id)
	}
	if first.title == "" {
		t.Error("title missing")
	}
	if !strings.Contains(first.url, "/updates/") {
		t.Errorf("url = %q", first.url)
	}
	if !strings.Contains(first.thumbnail, "/content/thumbs/") {
		t.Errorf("thumbnail = %q", first.thumbnail)
	}
	if len(first.performers) == 0 {
		t.Error("performers missing")
	}
	if first.duration == 0 {
		t.Error("duration missing")
	}
	if first.date == "" {
		t.Error("date missing")
	}
}

func TestContentID(t *testing.T) {
	cases := []struct{ thumb, url, want string }{
		{"https://site/content/thumbs/40593/preview-09.jpg", "https://site/updates/x", "40593"},
		{"", "https://site/updates/some-slug", "some-slug"},
		{"", "https://site/updates/some-slug/", "some-slug"},
	}
	for _, c := range cases {
		if got := contentID(c.thumb, c.url); got != c.want {
			t.Errorf("contentID(%q,%q) = %q, want %q", c.thumb, c.url, got, c.want)
		}
	}
}

func TestEnrichFromDetail(t *testing.T) {
	item := listItem{title: "Armpit Wors..."}
	enrichFromDetail(readFixture(t, "detail.html"), &item)
	// The card truncates a long title; the detail h1 carries it whole.
	if item.title != "Armpit Worship" {
		t.Errorf("title = %q", item.title)
	}
	if !strings.HasPrefix(item.description, "Girlfriends Daphne and Eve") {
		t.Errorf("description = %q", item.description)
	}
	if strings.Contains(item.description, "<") {
		t.Errorf("description still has markup: %q", item.description)
	}
	if len(item.tags) != 1 || item.tags[0] != "LesbianArmpit" {
		t.Errorf("tags = %v", item.tags)
	}
}

func TestEnrichFallsBackToMetaDescription(t *testing.T) {
	body := []byte(`<meta property="og:description" content="&lt;p style=&quot;text-align: center;&quot;&gt;Bailey pulls at the ropes.&lt;br&gt;Then more.&lt;/p&gt;">
<div class=" videoDescription ">
<p></p>
</div>`)
	var item listItem
	enrichFromDetail(body, &item)
	if item.description != "Bailey pulls at the ropes. Then more." {
		t.Errorf("description = %q", item.description)
	}
}

func TestEnrichKeepsCardValuesWhenDetailIsEmpty(t *testing.T) {
	item := listItem{title: "Card Title", description: "", tags: nil}
	enrichFromDetail([]byte("<html></html>"), &item)
	if item.title != "Card Title" {
		t.Errorf("title = %q", item.title)
	}
}

func TestParseModelfeatureListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing_modelfeature.html"))
	if len(items) != 3 {
		t.Fatalf("got %d cards, want 3", len(items))
	}
	first := items[0]
	if first.id != "105" || first.title != "Bent in Bondage with Orgasms" {
		t.Errorf("id/title = %q / %q", first.id, first.title)
	}
	if first.url != "https://fragileslave.com/updates/bent-in-bondage-with-orgasms" {
		t.Errorf("url = %q", first.url)
	}
	if len(first.performers) != 1 || first.performers[0] != "Nickey Huntsman" {
		t.Errorf("performers = %v", first.performers)
	}
	if first.duration != 6*60+48 {
		t.Errorf("duration = %d", first.duration)
	}
	if got := maxPage(readFixture(t, "listing_modelfeature.html")); got != 6 {
		t.Errorf("maxPage = %d, want 6", got)
	}
}

func TestEnrichModelfeatureDetail(t *testing.T) {
	var item listItem
	enrichFromDetail(readFixture(t, "detail_modelfeature.html"), &item)
	if item.title != "Bent in Bondage with Orgasms" {
		t.Errorf("title = %q", item.title)
	}
	if !strings.HasPrefix(item.description, "Nickey is as cute as they come") {
		t.Errorf("description = %q", item.description)
	}
	want := []string{"Bondage Orgasms", "Brunette", "All Natural", "Big Ass"}
	if strings.Join(item.tags, "|") != strings.Join(want, "|") {
		t.Errorf("tags = %v", item.tags)
	}
	if item.price != 6.99 {
		t.Errorf("price = %v", item.price)
	}
}

func TestMaxPage(t *testing.T) {
	if got := maxPage(readFixture(t, "listing_page1.html")); got < 2 {
		t.Errorf("maxPage = %d, want the pager's highest page", got)
	}
	// A pager the markup does not carry must read as unknown, not as page 1 —
	// that would stop the walk after one page.
	if got := maxPage([]byte("<html><a href='?page=9'>9</a></html>")); got != 0 {
		t.Errorf("maxPage without a pagination block = %d, want 0", got)
	}
}

func TestToScene(t *testing.T) {
	s := New(testSite())
	item := listItem{
		id: "40593", title: "Armpit Worship", url: "https://thesensitivespot.com/updates/x",
		thumbnail:  "https://thesensitivespot.com/content/thumbs/40593/preview-09.jpg",
		performers: []string{"Daphne Brooks"}, date: "Mar 14, 2026", duration: 414,
		price: 7.99, description: "D", tags: []string{"LesbianArmpit"},
	}
	sc := s.toScene(item, "https://thesensitivespot.com/", time.Now().UTC())
	if sc.SiteID != "thesensitivespot" || sc.Studio != "The Sensitive Spot" {
		t.Errorf("SiteID/Studio = %q/%q", sc.SiteID, sc.Studio)
	}
	if sc.Date.Format("2006-01-02") != "2026-03-14" {
		t.Errorf("Date = %v", sc.Date)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 7.99 {
		t.Errorf("PriceHistory = %v", sc.PriceHistory)
	}

	// A scene the site does not sell separately records no snapshot.
	item.price = 0
	if got := s.toScene(item, "https://thesensitivespot.com/", time.Now().UTC()); len(got.PriceHistory) != 0 {
		t.Errorf("PriceHistory = %v, want none", got.PriceHistory)
	}

	item.date = "not a date"
	if got := s.toScene(item, "https://thesensitivespot.com/", time.Now().UTC()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

func TestListScenes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/updates":
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		case strings.HasPrefix(r.URL.Path, "/updates/"):
			_, _ = w.Write(serveFixture(t, "detail.html", srv.URL))
		default:
			t.Errorf("unexpected fetch %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := testSite()
	cfg.SiteBase = srv.URL
	s := New(cfg)

	ch, err := s.ListScenes(context.Background(), "https://thesensitivespot.com/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []models.Scene
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindError:
			t.Errorf("error result: %v", res.Err)
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, sc := range scenes {
		if sc.Description == "" || len(sc.Tags) == 0 {
			t.Errorf("detail enrichment missing on %s", sc.ID)
		}
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/updates/") {
			_, _ = w.Write(serveFixture(t, "detail.html", srv.URL))
			return
		}
		_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
	}))
	defer srv.Close()

	known := parseListing(readFixture(t, "listing_page1.html"))[1].id
	cfg := testSite()
	cfg.SiteBase = srv.URL
	s := New(cfg)

	ch, _ := s.ListScenes(context.Background(), "https://thesensitivespot.com/",
		scraper.ListOpts{KnownIDs: map[string]bool{known: true}})
	scenes, stopped := 0, false
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if !stopped {
		t.Error("expected a StoppedEarly result")
	}
	if scenes != 1 {
		t.Errorf("scenes = %d, want 1", scenes)
	}
}

func TestTagURLScrapesThatTag(t *testing.T) {
	// The detail fetches run in a worker pool, so handler goroutines overlap
	// and the record of what was requested needs guarding.
	var (
		mu    sync.Mutex
		asked []string
	)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/updates/") {
			_, _ = w.Write(serveFixture(t, "detail.html", srv.URL))
			return
		}
		_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
	}))
	defer srv.Close()

	cfg := testSite()
	cfg.SiteBase = srv.URL
	s := New(cfg)

	ch, _ := s.ListScenes(context.Background(), "https://thesensitivespot.com/tags/lesbianarmpit", scraper.ListOpts{})
	for range ch {
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) == 0 || asked[0] != "/tags/lesbianarmpit" {
		t.Errorf("fetched %v, want the tag listing first", asked)
	}
}

func TestAgeGateEntersBeforeListing(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/enter" {
			http.SetCookie(w, &http.Cookie{Name: "age_verification_status", Value: "1", Path: "/"})
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		if _, err := r.Cookie("age_verification_status"); err != nil {
			http.Redirect(w, r, "/warning-page", http.StatusFound)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/updates/"):
			_, _ = w.Write(serveFixture(t, "detail.html", srv.URL))
		case r.URL.Path == "/updates":
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		default:
			_, _ = w.Write([]byte("<html>home</html>"))
		}
	}))
	defer srv.Close()

	count := func(gated bool) int {
		cfg := testSite()
		cfg.SiteBase = srv.URL
		cfg.AgeGate = gated
		ch, _ := New(cfg).ListScenes(context.Background(), srv.URL+"/", scraper.ListOpts{})
		n := 0
		for res := range ch {
			if res.Kind == scraper.KindScene {
				n++
			}
		}
		return n
	}
	if got := count(true); got != 2 {
		t.Errorf("with AgeGate: %d scenes, want 2", got)
	}
	if got := count(false); got != 0 {
		t.Errorf("without AgeGate: %d scenes, want 0", got)
	}
}

func TestListScenesReportsDetailFailureButKeepsScene(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/updates/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
	}))
	defer srv.Close()

	cfg := testSite()
	cfg.SiteBase = srv.URL
	s := New(cfg)

	ch, _ := s.ListScenes(context.Background(), "https://thesensitivespot.com/", scraper.ListOpts{})
	scenes, errs := 0, 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			errs++
		}
	}
	if scenes != 2 {
		t.Errorf("scenes = %d, want 2 — a dead detail page must not drop the card", scenes)
	}
	if errs != 2 {
		t.Errorf("errors = %d, want 2", errs)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

// serveFixture rewrites the live host in the fixture's self-links onto the test
// server, so the detail fetches stay offline.
func serveFixture(t *testing.T, name, base string) []byte {
	t.Helper()
	rewritten := strings.ReplaceAll(string(readFixture(t, name)), "https://thesensitivespot.com", base)
	if strings.Contains(rewritten, "https://thesensitivespot.com") {
		t.Fatalf("fixture %s still points at the live site", name)
	}
	return []byte(rewritten)
}
