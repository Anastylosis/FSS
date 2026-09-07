package brasileirinhas

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.brasileirinhas.com/", true},
		{"https://brasileirinhas.com/videos.html", true},
		// The .com.br host serves the same catalogue under Portuguese slugs.
		{"https://www.brasileirinhas.com.br/home.html", true},
		{"https://www.brasileirinhas.com/pornstar/elisa-sanches.html", true},
		{"https://brasileirinhas.com.evil.org/", false},
		{"https://acasadasbrasileirinhas.com.br/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// The slug in a scene URL is the localised title and differs between the .com
// and .com.br spellings of the same scene; the numeric suffix is the site's own
// content id and is the only stable key.
func TestSceneIDOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://www.brasileirinhas.com/video/-7527.html", "7527"},
		{"https://www.brasileirinhas.com/video/carol-corrales-gave-a-show-7527.html", "7527"},
		{"https://www.brasileirinhas.com.br/video/carol-corrales-deu-um-show-7527.html", "7527"},
		{"https://www.brasileirinhas.com/movie/meu-corno-minha-vida.html", ""},
		{"https://www.brasileirinhas.com/videos.html", ""},
	}
	for _, c := range cases {
		if got := sceneIDOf(c.in); got != c.want {
			t.Errorf("sceneIDOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

const detailPage = `<html><body>
<nav aria-label="breadcrumb"><ol class="breadcrumb">
	<li class="breadcrumb-item"><a href="/home.html">Home</a></li>
	<li class="breadcrumb-item"><a href="/videos.html">V&iacute;deos</a></li>
	<li class="breadcrumb-item active" aria-current="page">A Casa das Brasileirinhas Temporada 65</li>
</ol></nav>
<div class="row mb-4 caixaPlayer">
	<div class="col-8">
		<iframe class="iframeVideo" src='/play-2019.php?idVideo=7527'></iframe>
		<span class="tempoCena">01:23:00</span>
	</div>
	<div class="col-4 pr-0">
		<div class="sinopseVideo" style="color:#fff;">
			<h1 class="titleVideo">Carol Corrales deu um show no reality show porn&ocirc;</h1>
			This delicious came for the first time for the hottest reality show.
		</div>
	</div>
</div>
<ul class="ulTaguer">
	<li class="liTaguer"><a href="/videos/anal.html">Anal</a></li>
	<li class="liTaguer"><a href="/videos/big-cock.html">Big cock</a></li>
	<li class="liTaguer"><a href="/videos/anal.html">Anal</a></li>
</ul>
<ul class="tags"><li><a href="/videos/amateur.html">Amateur</a></li></ul>
</body></html>`

func TestParseScene(t *testing.T) {
	s := New()
	s.thumbBase = "https://cdn.example.com/player/"

	sc, err := s.parseScene([]byte(detailPage),
		"https://www.brasileirinhas.com/video/carol-corrales-7527.html", "https://www.brasileirinhas.com/")
	if err != nil {
		t.Fatal(err)
	}
	if sc.ID != "7527" || sc.SiteID != siteID {
		t.Errorf("id/site = %q/%q", sc.ID, sc.SiteID)
	}
	if sc.Title != "Carol Corrales deu um show no reality show pornô" {
		t.Errorf("title = %q", sc.Title)
	}
	// The synopsis block opens with the title heading; repeating it into the
	// description would put the title at the front of every scene's copy.
	if sc.Description != "This delicious came for the first time for the hottest reality show." {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.Duration != 3600+23*60 {
		t.Errorf("duration = %d", sc.Duration)
	}
	if sc.Series != "A Casa das Brasileirinhas Temporada 65" {
		t.Errorf("series = %q", sc.Series)
	}
	// Only the scene's own tag list counts; the site-wide tag nav sits in a
	// different list on the same page.
	if !slices.Equal(sc.Categories, []string{"Anal", "Big cock"}) {
		t.Errorf("categories = %v", sc.Categories)
	}
	if sc.Thumbnail != "https://cdn.example.com/player/7527.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	// Scene pages credit nobody.
	if len(sc.Performers) != 0 {
		t.Errorf("performers = %v, want none", sc.Performers)
	}
}

func TestParseSceneWithoutATitleIsAParseError(t *testing.T) {
	s := New()
	_, err := s.parseScene([]byte(`<html><body><p>nothing</p></body></html>`),
		"https://www.brasileirinhas.com/video/x-1.html", "https://www.brasileirinhas.com/")
	if err == nil {
		t.Fatal("want an error")
	}
	if k := scraper.Classify(err); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

// ---- end-to-end ----

type fakeSite struct {
	ids []int

	mu   sync.Mutex
	hits []string
}

func (f *fakeSite) hit(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits = append(f.hits, p)
}

func (f *fakeSite) hitList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.hits)
}

func (f *fakeSite) handler(t *testing.T, base func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.hit(r.URL.Path)
		switch {
		case r.URL.Path == "/sitemap.xml":
			var b strings.Builder
			b.WriteString(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
			fmt.Fprintf(&b, `<url><loc>%s/movie/some-release.html</loc></url>`, base())
			fmt.Fprintf(&b, `<url><loc>%s/pornstar/elisa-sanches.html</loc></url>`, base())
			for _, id := range f.ids {
				fmt.Fprintf(&b, `<url><loc>%s/video/scene-%d.html</loc></url>`, base(), id)
			}
			// A repeated entry must not become a second scene.
			if len(f.ids) > 0 {
				fmt.Fprintf(&b, `<url><loc>%s/video/scene-%d.html</loc></url>`, base(), f.ids[0])
			}
			b.WriteString(`</urlset>`)
			_, _ = fmt.Fprint(w, b.String())
		case strings.HasPrefix(r.URL.Path, "/pornstar/"), strings.HasPrefix(r.URL.Path, "/videos/"):
			var b strings.Builder
			b.WriteString(`<html><body><h1>Elisa Sanches</h1>`)
			for _, id := range f.ids {
				fmt.Fprintf(&b, `<a href="/video/scene-%d.html">x</a>`, id)
			}
			// Non-scene links on the same page must be ignored.
			b.WriteString(`<a href="/movie/some-release.html">movie</a></body></html>`)
			_, _ = fmt.Fprint(w, b.String())
		case strings.HasPrefix(r.URL.Path, "/video/"):
			id := sceneIDOf(r.URL.Path)
			_, _ = fmt.Fprint(w, strings.Replace(detailPage,
				"Carol Corrales deu um show no reality show porn&ocirc;", "Scene "+id, 1))
		default:
			http.NotFound(w, r)
		}
	}
}

func newTestScraper(t *testing.T, f *fakeSite) *Scraper {
	t.Helper()
	var s *Scraper
	ts := httptest.NewServer(f.handler(t, func() string { return s.base }))
	t.Cleanup(ts.Close)
	s = &Scraper{Client: ts.Client(), base: ts.URL, thumbBase: "https://cdn.example.com/player/"}
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string) ([]models.Scene, []error) {
	t.Helper()
	out := make(chan scraper.SceneResult, 500)
	go s.run(context.Background(), studioURL, scraper.ListOpts{}, out)
	var scenes []models.Scene
	var errs []error
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	return scenes, errs
}

func ids(scenes []models.Scene) []string {
	out := make([]string, len(scenes))
	for i, sc := range scenes {
		out[i] = sc.ID
	}
	slices.Sort(out)
	return out
}

func TestRunWalksTheSitemap(t *testing.T) {
	f := &fakeSite{ids: []int{7527, 7528, 7529}}
	s := newTestScraper(t, f)

	scenes, errs := collect(t, s, "https://www.brasileirinhas.com/")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !slices.Equal(ids(scenes), []string{"7527", "7528", "7529"}) {
		t.Errorf("ids = %v", ids(scenes))
	}
	// The sitemap also lists movies and performers; only /video/ pages are
	// fetched as scenes.
	if slices.Contains(f.hitList(), "/movie/some-release.html") {
		t.Error("fetched a movie page as if it were a scene")
	}
}

// The performer's name is only stated on their own page — scene pages credit
// nobody — so scraping a performer URL is the one mode that yields a cast.
func TestRunPerformerPageCreditsThePerformer(t *testing.T) {
	f := &fakeSite{ids: []int{8889, 8880}}
	s := newTestScraper(t, f)

	scenes, errs := collect(t, s, s.base+"/pornstar/elisa-sanches.html")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, sc := range scenes {
		if !slices.Equal(sc.Performers, []string{"Elisa Sanches"}) {
			t.Errorf("scene %s performers = %v", sc.ID, sc.Performers)
		}
	}
	if slices.Contains(f.hitList(), "/sitemap.xml") {
		t.Error("a performer walk must not fall back to the whole catalogue")
	}
}

// A category page has no performer to credit, so it must not borrow one.
func TestRunCategoryPageCreditsNobody(t *testing.T) {
	f := &fakeSite{ids: []int{8958}}
	s := newTestScraper(t, f)

	scenes, errs := collect(t, s, s.base+"/videos/anal.html")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if len(scenes[0].Performers) != 0 {
		t.Errorf("performers = %v, want none", scenes[0].Performers)
	}
}

func TestRunReportsASitemapWithNoScenes(t *testing.T) {
	f := &fakeSite{}
	s := newTestScraper(t, f)

	scenes, errs := collect(t, s, "https://www.brasileirinhas.com/")
	if len(scenes) != 0 {
		t.Errorf("got %d scenes, want none", len(scenes))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}
