package classmedia

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestCleanText(t *testing.T) {
	cases := map[string]string{
		"<h1>Hello <b>World</b></h1>": "Hello World",
		"  Tom &amp; Jerry  ":         "Tom & Jerry",
		"a\n\tb   c":                  "a b c",
	}
	for in, want := range cases {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeslug(t *testing.T) {
	cases := map[string]string{
		"cosplay-love":   "Cosplay Love",
		"a-b-c":          "A B C",
		"single":         "Single",
		"trailing-dash-": "Trailing Dash",
		"":               "",
	}
	for in, want := range cases {
		if got := deslug(in); got != want {
			t.Errorf("deslug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSslID(t *testing.T) {
	cases := map[string]string{
		"https://www.subspaceland.com/video/jane/the-scene/": "jane/the-scene",
		"https://www.subspaceland.com/video/jane/the-scene":  "jane/the-scene",
		"no-video-segment": "no-video-segment",
	}
	for in, want := range cases {
		if got := sslID(in); got != want {
			t.Errorf("sslID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	ssl := NewSubspaceland()
	oj := NewOldje()
	o3 := NewOldje3some()
	if !ssl.MatchesURL("https://www.subspaceland.com/video/x/y") {
		t.Error("subspaceland should match")
	}
	if !oj.MatchesURL("http://oldje.com/gallery/2") {
		t.Error("oldje should match")
	}
	if !o3.MatchesURL("https://www.oldje-3some.com/videos") {
		t.Error("oldje3some should match")
	}
	if oj.MatchesURL("https://www.oldje-3some.com/") {
		t.Error("oldje must not match oldje-3some")
	}
}

// ---- Subspaceland detail parsing ----

const subspacelandDetailHTML = `<html><head>
<meta name="description" content="A tense &amp; intimate session.">
</head><body>
<h1>The Binding <span>Session</span></h1>
<div class="meta">Released on 14 Mar 2025</div>
<a href="https://www.subspaceland.com/model/jane-doe">Jane Doe</a>
<a href="https://www.subspaceland.com/tag/rope">Rope</a>
<a href="https://www.subspaceland.com/tag/bondage">Bondage</a>
<a href="https://www.subspaceland.com/tag/rope">Rope</a>
<img src="/sets/4096/main.jpg">
</body></html>`

func TestScrapeSubspacelandDetail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, subspacelandDetailHTML)
	}))
	defer ts.Close()

	s := NewSubspaceland()
	s.Client = ts.Client()
	now := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)

	detailURL := ts.URL + "/video/jane-doe/the-binding-session"
	scene, ok := s.scrapeSubspacelandDetail(context.Background(), "https://www.subspaceland.com", detailURL, now)
	if !ok {
		t.Fatal("scrapeSubspacelandDetail returned ok=false")
	}
	if scene.Title != "The Binding Session" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.ID != "jane-doe/the-binding-session" {
		t.Errorf("ID = %q", scene.ID)
	}
	wantDate := time.Date(2025, 3, 14, 0, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", scene.Date, wantDate)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Jane Doe" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if len(scene.Tags) != 2 || scene.Tags[0] != "Rope" || scene.Tags[1] != "Bondage" {
		t.Errorf("Tags = %v, want dedup [Rope Bondage]", scene.Tags)
	}
	if scene.Thumbnail != "https://www.subspaceland.com/sets/4096/mov_img/movie_preview.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if scene.Description != "A tense & intimate session." {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Studio != "Subspaceland" {
		t.Errorf("Studio = %q", scene.Studio)
	}
}

func TestScrapeSubspacelandDetail_noTitleDropped(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "<html><body>no heading here</body></html>")
	}))
	defer ts.Close()

	s := NewSubspaceland()
	s.Client = ts.Client()
	_, ok := s.scrapeSubspacelandDetail(context.Background(), "x", ts.URL+"/video/a/b", time.Now())
	if ok {
		t.Error("expected ok=false when no <h1> title")
	}
}

func TestFetchSitemap_dedupsAndNormalizes(t *testing.T) {
	sitemap := `<?xml version="1.0"?><urlset>
<url><loc>http://www.subspaceland.com/video/jane/scene-one</loc></url>
<url><loc>https://www.subspaceland.com/video/jane/scene-one</loc></url>
<url><loc>https://subspaceland.com/video/mary/scene-two</loc></url>
</urlset>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sitemap.xml" {
			_, _ = fmt.Fprint(w, sitemap)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	s := NewSubspaceland()
	s.Client = ts.Client()
	s.cfg.base = ts.URL

	urls, err := s.fetchSitemap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// http:// normalized to https:// then deduped -> 2 unique.
	if len(urls) != 2 {
		t.Fatalf("got %d urls, want 2: %v", len(urls), urls)
	}
	for _, u := range urls {
		if u[:8] != "https://" {
			t.Errorf("url not normalized to https: %q", u)
		}
	}
}

// ---- Oldje listing parsing ----

const oldjeGalleryHTML = `<html><body>
<div class="set"><img src="/sets/537/cosplay-love.webp"></div>
<div class="set"><img src="/sets/538/the-old-painter.webp"></div>
<div class="set"><img src="/sets/537/cosplay-love.webp"></div>
</body></html>`

func TestRunOldje(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/gallery/1":
			_, _ = fmt.Fprint(w, oldjeGalleryHTML)
		default:
			_, _ = fmt.Fprint(w, "<html><body></body></html>")
		}
	}))
	defer ts.Close()

	s := NewOldje()
	s.Client = ts.Client()
	s.cfg.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			got[r.Scene.ID] = r.Scene.Title
			if r.Scene.Thumbnail == "" {
				t.Errorf("scene %s missing thumbnail", r.Scene.ID)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2 (deduped): %v", len(got), got)
	}
	if got["537"] != "Cosplay Love" || got["538"] != "The Old Painter" {
		t.Errorf("scenes = %v", got)
	}
}

// ---- Oldje-3some listing parsing ----

const oldje3someListHTML = `<html><body>
<script>
window.sslSearchItems = [{"title":"","actors":"Oldje-3Some","duration":"24:23","thumb":"/media/sets/188/cover.jpg","url":"/videos/8ff48390","search":""},{"title":"Charge Me Up","actors":"Victoria Benz, Benito","duration":"22:30","thumb":"/media/sets/187/cover.jpg","url":"/videos/c0be3b5a","search":"charge me up 187"},{"title":"Charge Me Up","actors":"Victoria Benz","duration":"22:30","thumb":"/media/sets/187/cover.jpg","url":"/videos/c0be3b5a","search":"dupe"}];
</script>
</body></html>`

// Detail pages, trimmed from live 2026-10 markup. 188 is one of the newest
// sets, which the site publishes with an empty title and empty cast names.
const oldje3someDetail188 = `<section class='ssl-detail-page'><div class='ssl-detail-main'><div class='ssl-detail-content'><h1></h1><div class='ssl-detail-meta'><span><i class='bi bi-calendar3'></i> Jul 31, 2026</span><span><i class='bi bi-badge-4k'></i> 4K UHD</span></div>
<div class='ssl-detail-section'><h2>About This Update</h2><p>Experience the newest Oldje-3Some update.</p><div class='ssl-detail-tags'>
	<span>double-blowjob</span>
	<span>massage</span>
</div></div>
<div class='ssl-detail-section'><h2>Featured Performers</h2><div class='ssl-detail-performer-grid'><a href='/girls/mia-de-vil'><img src='/a.jpg' alt='' loading='lazy'><strong></strong></a><a href='/oldjes/harry-river'><img src='/b.jpg' alt=''><strong></strong></a></div></div>
<div class='ssl-detail-related'><h2>Related Scenes</h2><div class='ssl-detail-related-grid'><a href='/videos/98'><strong>A Dick Is A Dick</strong><span>Amy Douxxx, Missy Luv, Master X</span></a></div></div>
</div></div></section>`

const oldje3someDetail187 = `<div class='ssl-detail-content'><h1>Charge Me Up</h1><div class='ssl-detail-meta'><span><i class='bi bi-calendar3'></i> May 5, 2025</span></div>
<div class='ssl-detail-tags'><span>kissing</span><span>teen</span></div>
<div class='ssl-detail-performer-grid'><a href='/girls/victoria-benz'><strong>Victoria Benz</strong></a><a href='/oldjes/benito'><strong>Benito</strong></a><a href='/oldjes/erik'><strong></strong></a></div>
<div class='ssl-detail-related-grid'><a href='/videos/c5'><strong>Picture This</strong></a></div></div>`

func TestRunOldje3some(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/gallery/1":
			_, _ = fmt.Fprint(w, oldje3someListHTML)
		case "/videos/8ff48390":
			_, _ = fmt.Fprint(w, oldje3someDetail188)
		case "/videos/c0be3b5a":
			_, _ = fmt.Fprint(w, oldje3someDetail187)
		default:
			_, _ = fmt.Fprint(w, "<html><body></body></html>")
		}
	}))
	defer ts.Close()

	s := NewOldje3some()
	s.Client = ts.Client()
	s.cfg.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := map[string]models.Scene{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes[r.Scene.ID] = r.Scene
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (deduped by set id): %v", len(scenes), scenes)
	}
	// The site publishes no title for its newest sets; none is invented.
	if got := scenes["188"].Title; got != "" {
		t.Errorf("title for 188 = %q, want empty", got)
	}
	if got := scenes["188"].URL; got != ts.URL+"/videos/8ff48390" {
		t.Errorf("URL for 188 = %q", got)
	}
	if got := scenes["188"].Duration; got != 24*60+23 {
		t.Errorf("duration for 188 = %d, want %d", got, 24*60+23)
	}
	if got := scenes["188"].Date; !got.Equal(time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date for 188 = %v", got)
	}
	if got := scenes["188"].Tags; len(got) != 2 || got[0] != "double-blowjob" || got[1] != "massage" {
		t.Errorf("tags for 188 = %v", got)
	}
	// The site puts its own name in "actors" when there is no real cast, and
	// blank <strong> names on the detail page must not become performers.
	if got := scenes["188"].Performers; len(got) != 0 {
		t.Errorf("performers for 188 = %v, want none", got)
	}
	sc := scenes["187"]
	if sc.Title != "Charge Me Up" || !sc.Date.Equal(time.Date(2025, 5, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("187 = %q %v", sc.Title, sc.Date)
	}
	if got := sc.Performers; len(got) != 2 || got[0] != "Victoria Benz" || got[1] != "Benito" {
		t.Errorf("performers for 187 = %v, want [Victoria Benz Benito]", got)
	}
}

// A detail page without a release date is reported as a parse error, and the
// scene is still emitted with its listing data.
func TestRunOldje3someDetailParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/gallery/1" {
			_, _ = fmt.Fprint(w, oldje3someListHTML)
			return
		}
		_, _ = fmt.Fprint(w, "<html><body>redesigned</body></html>")
	}))
	defer ts.Close()

	s := NewOldje3some()
	s.Client = ts.Client()
	s.cfg.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{KnownIDs: map[string]bool{"188": true}})
	if err != nil {
		t.Fatal(err)
	}
	var scenes, errs int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			errs++
			if scraper.Classify(r.Err) != scraper.FailureParse {
				t.Errorf("Classify = %v, want FailureParse", scraper.Classify(r.Err))
			}
		}
	}
	// 188 is known: no detail fetch, no emit. 187 fails its detail but is kept.
	if scenes != 1 || errs != 1 {
		t.Errorf("got %d scenes, %d errors; want 1, 1", scenes, errs)
	}
}

// TestRunOldje3someMissingBlock pins that a listing without the JSON block
// reports a parse error rather than scraping 0 scenes silently.
func TestRunOldje3someMissingBlock(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><body>no block</body></html>")
	}))
	defer ts.Close()

	s := NewOldje3some()
	s.Client = ts.Client()
	s.cfg.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
			if scraper.Classify(r.Err) != scraper.FailureParse {
				t.Errorf("Classify = %v, want FailureParse", scraper.Classify(r.Err))
			}
		}
	}
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}
