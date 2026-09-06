package meninosonline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.meninosonline.net/", true},
		{"https://meninosonline.net/en/movies", true},
		{"https://www.meninosonline.net/en/player/some-scene", true},
		{"https://meninosonline.net.evil.org/", false},
		{"https://meninos.net/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestListingURL(t *testing.T) {
	s := New()
	s.base = "https://x"
	if got := s.listingURL(1); got != "https://x/en/movies" {
		t.Errorf("page 1 = %q", got)
	}
	if got := s.listingURL(4); got != "https://x/en/movies?page=4" {
		t.Errorf("page 4 = %q", got)
	}
}

func listCard(slug, title, thumb string) string {
	return fmt.Sprintf(`<div class="pnl-mini">
	<a style="cursor: pointer;" data-turbolinks="false" href="/en/player/%s">
	<figure><i class="fa fa-play-circle-o fa-2x"></i>
	<img src="%s" alt="Capa 01" />
	<figcaption>%s</figcaption>
	</figure></a></div>`, slug, thumb, title)
}

func TestParseListing(t *testing.T) {
	page := "<html><body>" +
		listCard("a-b-scene-one", "Aquele Ton &amp; Luiz Felipe - Cuidando do Cunhadinho",
			"https://s3-us-west-2.amazonaws.com/meninoson/videos/images/000/002/732/medium/capa_01.jpg?1") +
		listCard("c-d-scene-two", "Peuops Ramos &amp; Felipe GoodBoy", "/assets/relative.jpg") +
		// The same card twice must not become two scenes.
		listCard("a-b-scene-one", "Aquele Ton &amp; Luiz Felipe - Cuidando do Cunhadinho", "/x.jpg") +
		"</body></html>"

	items := parseListing([]byte(page), "https://www.meninosonline.net")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].slug != "a-b-scene-one" {
		t.Errorf("slug = %q", items[0].slug)
	}
	if items[0].title != "Aquele Ton & Luiz Felipe - Cuidando do Cunhadinho" {
		t.Errorf("title = %q", items[0].title)
	}
	if items[0].thumbnail != "https://s3-us-west-2.amazonaws.com/meninoson/videos/images/000/002/732/medium/capa_01.jpg?1" {
		t.Errorf("thumbnail = %q", items[0].thumbnail)
	}
	// A site-relative thumbnail is resolved against the site, not stored bare.
	if items[1].thumbnail != "https://www.meninosonline.net/assets/relative.jpg" {
		t.Errorf("relative thumbnail = %q", items[1].thumbnail)
	}
}

func TestLastPage(t *testing.T) {
	body := []byte(`<a href="/en/movies?page=2">2</a><a href="/en/movies?page=51">51</a>`)
	if got := lastPage(body); got != 51 {
		t.Errorf("lastPage = %d, want 51", got)
	}
	if got := lastPage([]byte(`<div>no pager</div>`)); got != 1 {
		t.Errorf("lastPage = %d, want 1", got)
	}
}

// The cast is only ever named as data in the "related scenes" headings; the
// title runs the performers together with an ampersand.
func TestParseCast(t *testing.T) {
	body := []byte(`<h2>RELATED SCENES</h2>
	<div class="pnl-extra">
	<h4 class="d-1">Aquele Ton (2)</h4>
	<div class="pnl-mini"><h5 class="text-right">2026-02-10</h5></div>
	<h4 class="d-1"> Luiz Felipe (11)</h4>
	<h4 class="d-1">Aquele Ton (2)</h4>
	</div>`)
	got := parseCast(body)
	if !slices.Equal(got, []string{"Aquele Ton", "Luiz Felipe"}) {
		t.Errorf("parseCast = %v", got)
	}
	if got := parseCast([]byte(`<div>no related block</div>`)); len(got) != 0 {
		t.Errorf("parseCast = %v, want none", got)
	}
}

// ---- end-to-end ----

type fakeSite struct {
	pages   int
	per     int
	noPager bool

	mu      sync.Mutex
	listing []int
	details int
}

func (f *fakeSite) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/en/player/") {
			f.mu.Lock()
			f.details++
			f.mu.Unlock()
			_, _ = fmt.Fprint(w, `<h2>RELATED SCENES</h2><div class="pnl-extra">
				<h4 class="d-1">Alice (3)</h4><h4 class="d-1">Bob (5)</h4></div>
				<img src="https://s3.example.com/meninoson/videos/images/000/1/original/capa_01.jpg?1" />`)
			return
		}
		if r.URL.Path != listPath {
			http.NotFound(w, r)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		f.mu.Lock()
		f.listing = append(f.listing, page)
		f.mu.Unlock()

		var b strings.Builder
		if page <= f.pages {
			for i := range f.per {
				slug := fmt.Sprintf("p%d-s%d", page, i)
				b.WriteString(listCard(slug, "Scene "+slug, "/t/"+slug+".jpg"))
			}
		}
		if !f.noPager {
			for p := 1; p <= f.pages; p++ {
				fmt.Fprintf(&b, `<a href="/en/movies?page=%d">%d</a>`, p, p)
			}
		}
		_, _ = fmt.Fprint(w, b.String())
	}
}

func newTestScraper(t *testing.T, f *fakeSite) *Scraper {
	t.Helper()
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	return &Scraper{Client: ts.Client(), base: ts.URL}
}

func collect(t *testing.T, s *Scraper, opts scraper.ListOpts) ([]string, []error) {
	t.Helper()
	out := make(chan scraper.SceneResult, 500)
	go s.run(context.Background(), "https://www.meninosonline.net/", opts, out)
	var ids []string
	var errs []error
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	return ids, errs
}

func TestRunWalksToTheLastPage(t *testing.T) {
	f := &fakeSite{pages: 3, per: 2}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(ids) != 6 {
		t.Errorf("got %d scenes, want 6", len(ids))
	}
	if f.details != 6 {
		t.Errorf("fetched %d detail pages for %d scenes", f.details, len(ids))
	}
	if slices.Contains(f.listing, 4) {
		t.Errorf("requested a page past the pager's last: %v", f.listing)
	}
}

// A pager naming only page 1 is indistinguishable from no pager at all, so it
// must not end the walk after one page — the empty-page stop takes over.
func TestRunFallsBackToAnEmptyPageWithoutAPager(t *testing.T) {
	f := &fakeSite{pages: 2, per: 2, noPager: true}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(ids) != 4 {
		t.Errorf("got %d scenes, want 4 — the walk stopped after page one", len(ids))
	}
}

func TestRunReportsAnEmptyFirstPage(t *testing.T) {
	f := &fakeSite{pages: 0, per: 0}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, scraper.ListOpts{})
	if len(ids) != 0 {
		t.Errorf("got %d scenes, want none", len(ids))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

// A detail page that fails costs the cast, not the scene.
func TestRunKeepsSceneWhenTheDetailPageFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/en/player/") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if r.URL.Query().Get("page") != "" {
			_, _ = fmt.Fprint(w, `<div class="empty"></div>`)
			return
		}
		_, _ = fmt.Fprint(w, listCard("only", "Only Scene", "/t.jpg"))
	}))
	defer ts.Close()
	s := &Scraper{Client: ts.Client(), base: ts.URL}

	ids, errs := collect(t, s, scraper.ListOpts{})
	if !slices.Equal(ids, []string{"only"}) {
		t.Errorf("ids = %v", ids)
	}
	if len(errs) != 1 {
		t.Errorf("got %d errors, want the detail failure reported", len(errs))
	}
}
