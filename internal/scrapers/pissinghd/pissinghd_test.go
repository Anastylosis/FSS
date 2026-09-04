package pissinghd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

var _ scraper.StudioScraper = (*Scraper)(nil)

const testCard1 = `<div class="col-md-4 col-xs-12 col-sm-6"><!-- Thumbs -->
  <div align="center">
    <a class="fancybox thumbs" href="#inline30965" data-id="30965">
      <img src="https://cdn.example.com/play.png" class="overlay-btn">
      <img src="https://cdn.example.com/thumb1.jpg" class="img-responsive thumb" />
    </a>
  </div>
  <div class="col-md-12 tit-main">
    <div class="tit-title one-liner">
      <div align="center">First Scene Title</div>
    </div>
    <div class="tit-desc">
      <div id="episodedesc30965" class="panel-collapse collapse">
        <p class="western style3">Description of the first scene.</p>
      </div>
    </div>
  </div>
  <!-- End Thumbs -->
</div>`

const testCard2 = `<div class="col-md-4 col-xs-12 col-sm-6"><!-- Thumbs -->
  <div align="center">
    <a class="fancybox thumbs" href="#inline30966" data-id="30966">
      <img src="https://cdn.example.com/play.png" class="overlay-btn">
      <img src="https://cdn.example.com/thumb2.jpg" class="img-responsive thumb" />
    </a>
  </div>
  <div class="col-md-12 tit-main">
    <div class="tit-title one-liner">
      <div align="center">Second Scene Title</div>
    </div>
    <div class="tit-desc">
      <div id="episodedesc30966" class="panel-collapse collapse">
        <p class="western style3">Description with <b>HTML</b> tags.</p>
      </div>
    </div>
  </div>
  <!-- End Thumbs -->
</div>`

func TestParseListingPage(t *testing.T) {
	body := []byte(testCard1 + testCard2)
	items := parseListingPage(body)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	s := items[0]
	if s.id != "30965" {
		t.Errorf("id = %q, want 30965", s.id)
	}
	if s.title != "First Scene Title" {
		t.Errorf("title = %q", s.title)
	}
	if s.thumbnail != "https://cdn.example.com/thumb1.jpg" {
		t.Errorf("thumbnail = %q", s.thumbnail)
	}
	if s.description != "Description of the first scene." {
		t.Errorf("description = %q", s.description)
	}

	s2 := items[1]
	if s2.description != "Description with HTML tags." {
		t.Errorf("description = %q, want HTML stripped", s2.description)
	}
}

func TestRun(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		page := r.URL.Query().Get("page")
		switch page {
		case "", "1":
			_, _ = fmt.Fprint(w, testCard1+testCard2)
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Delay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2", len(got))
	}
	if got[0].SiteID != "pissinghd" {
		t.Errorf("siteID = %q", got[0].SiteID)
	}
	if got[0].Title != "First Scene Title" {
		t.Errorf("title = %q", got[0].Title)
	}
}

func TestKnownIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, testCard1+testCard2)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"30966": true},
		Delay:    time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, stopped := testutil.CollectScenesWithStop(t, ch)
	if len(got) != 1 {
		t.Fatalf("got %d scenes, want 1", len(got))
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
}

// Scenes used to be stored with the listing page they happened to appear on
// ("/videos?page=3"), so every scene on a page shared one URL and, as the
// catalogue grew and cards shifted between pages, a stored URL started naming
// a different scene. The tour publishes no per-scene page at all, so the card's
// own inline anchor is the only stable address.
func TestSceneURLsAreStableAndPerScene(t *testing.T) {
	items := parseListingPage([]byte(twoCardListing))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	seen := map[string]bool{}
	for _, item := range items {
		u := fmt.Sprintf("%s/videos#inline%s", tourBase, item.id)
		if seen[u] {
			t.Errorf("two scenes share the URL %q", u)
		}
		seen[u] = true
		if strings.Contains(u, "page=") {
			t.Errorf("URL %q still embeds the listing page number", u)
		}
	}
}

const twoCardListing = `<html><body>
<div class="col-md-4 col-xs-12 col-sm-6"><!-- Thumbs -->
	<a class="fancybox thumbs" href="#inline30965" data-id="30965">
		<img src="https://cdn.example.com/a.jpg" class="img-responsive thumb" />
	</a>
	<div class="col-md-12 tit-main">
		<div class="tit-title one-liner"><div align="center">Scene A</div></div>
		<div id="episodedesc30965"><p class="western">Description A.</p></div>
	</div>
	<!-- End Thumbs -->
</div>
<div class="col-md-4 col-xs-12 col-sm-6"><!-- Thumbs -->
	<a class="fancybox thumbs" href="#inline30966" data-id="30966">
		<img src="https://cdn.example.com/b.jpg" class="img-responsive thumb" />
	</a>
	<div class="col-md-12 tit-main">
		<div class="tit-title one-liner"><div align="center">Scene B</div></div>
		<div id="episodedesc30966"><p class="western">Description B.</p></div>
	</div>
	<!-- End Thumbs -->
</div>
</body></html>`
