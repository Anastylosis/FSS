package privateblack

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

const listingHTML = `<html><body>
<ul class='thumb-list scenes clearfix'>

<li class=" col-md-3 col-xs-6 col-sm-6 col-xs-12">
  <div class="scene">
    <a href="https://www.privateblack.com/scene/winnie-dp-in-the-office/1282" title="Winnie, DP in the Office HD Videos &amp; Porn Photos - Private Sex">
      <picture>
        <source media="(max-width: 360px)" srcset="https://pblack77.st-content.com/content/contentthumbs/10505.jpg?secure=foo 260w" sizes="100vw">
        <img srcset="https://pblack77.st-content.com/content/contentthumbs/10502.jpg?secure=bar 460w" src="https://pblack77.st-content.com/content/contentthumbs/10499.jpg?secure=baz">
      </picture>
    </a>
    <ul class="scene-details clearfix">
      <li class="hdlabel"><a href="https://www.privateblack.com/scene/winnie-dp-in-the-office/1282"><span>HD</span></a></li>
    </ul>
    <div>
      <h3>
        <a href="https://www.privateblack.com/scene/winnie-dp-in-the-office/1282">Winnie, DP in the Office &amp; More</a>
      </h3>
      <ul class="scene-models">
        <li><a href="https://www.privateblack.com/pornstar/373-winnie/">Winnie</a></li>
      </ul>
      <span class="scene-date">05/25/2026</span>
    </div>
  </div>
</li>

<li class=" col-md-3 col-xs-6 col-sm-6 col-xs-12">
  <div class="scene">
    <a href="https://www.privateblack.com/scene/double-trouble/1275">
      <picture>
        <img srcset="https://pblack77.st-content.com/content/contentthumbs/10400.jpg?secure=xyz 460w" src="https://pblack77.st-content.com/content/contentthumbs/10399.jpg">
      </picture>
    </a>
    <div>
      <h3><a href="https://www.privateblack.com/scene/double-trouble/1275">Double Trouble</a></h3>
      <ul class="scene-models">
        <li><a href="https://www.privateblack.com/pornstar/100-sara/">Sara Black</a></li>
        <li><a href="https://www.privateblack.com/pornstar/101-mai/">Mai White</a></li>
      </ul>
      <span class="scene-date">04/01/2026</span>
    </div>
  </div>
</li>

</ul>

<ul class="pagination">
  <li><span class="current">1</span></li>
  <li><a href="https://www.privateblack.com/scenes/2/">2</a></li>
  <li><a href="https://www.privateblack.com/scenes/18/">18</a></li>
</ul>
</body></html>`

// Page-2 fixture — one final scene; the next page returns empty.
const listing2HTML = `<html><body>
<div class="scene">
  <a href="https://www.privateblack.com/scene/last-one/1200">
    <picture><img src="https://pblack77.st-content.com/content/contentthumbs/9000.jpg"></picture>
  </a>
  <h3><a href="https://www.privateblack.com/scene/last-one/1200">Last One</a></h3>
  <ul class="scene-models">
    <li><a href="https://www.privateblack.com/pornstar/200-x/">Last Lady</a></li>
  </ul>
  <span class="scene-date">02/14/2026</span>
</div>
</body></html>`

const emptyHTML = `<html><body><div class="container">no scenes</div></body></html>`

func TestParseListing(t *testing.T) {
	items := parseListing([]byte(listingHTML))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	first := items[0]
	if first.id != "1282" {
		t.Errorf("ID = %q, want 1282", first.id)
	}
	if first.url != "https://www.privateblack.com/scene/winnie-dp-in-the-office/1282" {
		t.Errorf("URL = %q", first.url)
	}
	if first.title != "Winnie, DP in the Office & More" {
		t.Errorf("Title = %q (entity unescape failed?)", first.title)
	}
	if len(first.performers) != 1 || first.performers[0] != "Winnie" {
		t.Errorf("Performers = %v", first.performers)
	}
	if first.date.Year() != 2026 || first.date.Month() != 5 || first.date.Day() != 25 {
		t.Errorf("Date = %v, want 2026-05-25", first.date)
	}
	// Thumbnail comes from <img srcset="..."> when present (preferred).
	if !strings.HasPrefix(first.thumb, "https://pblack77.st-content.com/content/contentthumbs/10502") {
		t.Errorf("Thumb = %q (expected srcset URL)", first.thumb)
	}

	second := items[1]
	if second.id != "1275" {
		t.Errorf("Second ID = %q", second.id)
	}
	if len(second.performers) != 2 {
		t.Errorf("Second performers = %v (want 2)", second.performers)
	}
}

func TestEstimateTotal(t *testing.T) {
	// pagination lists pages 1, 2, 18 → max 18 × 2 cards = 36.
	got := estimateTotal([]byte(listingHTML), 2)
	if got != 36 {
		t.Errorf("estimateTotal = %d, want 36", got)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://www.privateblack.com/", true},
		{"https://privateblack.com/scenes", true},
		{"http://privateblack.com/scenes/2/", true},
		{"https://example.com/", false},
		{"https://www.private.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.ok {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.ok)
		}
	}
}

func TestListingURL(t *testing.T) {
	s := New()
	if got := s.listingURL(1); got != "https://www.privateblack.com/scenes" {
		t.Errorf("page 1 → %q", got)
	}
	if got := s.listingURL(5); got != "https://www.privateblack.com/scenes/5/" {
		t.Errorf("page 5 → %q", got)
	}
}

func TestListScenes_endToEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/scenes":
			_, _ = fmt.Fprint(w, listingHTML)
		case "/scenes/2/":
			_, _ = fmt.Fprint(w, listing2HTML)
		default:
			_, _ = fmt.Fprint(w, emptyHTML)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes, total int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.Studio != "Private" {
				t.Errorf("Studio = %q", r.Scene.Studio)
			}
			if r.Scene.Series != "Private Black" {
				t.Errorf("Series = %q", r.Scene.Series)
			}
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 3 {
		t.Errorf("got %d scenes, want 3", scenes)
	}
	if total != 36 {
		t.Errorf("total = %d, want 36", total)
	}
}

func TestListScenes_knownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/scenes" {
			_, _ = fmt.Fprint(w, listingHTML)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{
		KnownIDs: map[string]bool{"1275": true},
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
		t.Errorf("got %d scenes, want 1 (stop before known)", scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
}

func TestListScenes_pornstarPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/pornstar/363-milenaray/":
			_, _ = fmt.Fprint(w, listingHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/pornstar/363-milenaray/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

// A page that loads but yields no cards is a parser or redesign failure, not an
// empty catalogue: reported silently it would let an authoritative Save delete
// every stored scene.
func TestListScenes_emptyPageIsAParseError(t *testing.T) {
	for _, tt := range []struct{ name, path string }{
		{"catalogue", "/scenes"},
		{"pornstar", "/pornstar/363-milenaray/"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, `<html><body><div class="redesigned"></div></body></html>`)
			}))
			defer ts.Close()

			s := &Scraper{client: ts.Client(), base: ts.URL}
			ch, err := s.ListScenes(context.Background(), ts.URL+tt.path, scraper.ListOpts{})
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
		})
	}
}
