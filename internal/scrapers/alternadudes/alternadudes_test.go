package alternadudes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

var _ scraper.StudioScraper = (*Scraper)(nil)

const testCard = `<div class="item col-lg-4 col-md-4 col-12 padx">
  <div class="product-item">
    <div class="pi-img-wrapper">
      <a href="https://www.alternadudes.com/trailers/Test-Scene.html"
         onclick="tload('/trailers/test-trailer.mp4'); return false;">
        <img src="/content/contentthumbs/54/56/15456-1x.jpg" alt="Test Scene" class="img-fluid">
      </a>
    </div>
  </div>
</div>`

const testCardNoTrailer = `<div class="item col-lg-4 col-md-4 col-12 padx">
  <div class="product-item">
    <div class="pi-img-wrapper">
      <a href="https://secure.alternadudes.com/signup/signup.php">
        <img src="/content/contentthumbs/12/34/12345-1x.jpg" alt="Old Scene" class="img-fluid">
      </a>
    </div>
  </div>
</div>`

func TestParseListingPage(t *testing.T) {
	body := []byte(testCard + testCardNoTrailer)
	entries := parseListingPage(body, "https://www.alternadudes.com/categories/movies.html")
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	e := entries[0]
	if e.id != "15456" {
		t.Errorf("id = %q, want 15456", e.id)
	}
	if e.title != "Test Scene" {
		t.Errorf("title = %q, want Test Scene", e.title)
	}
	if e.thumbnail != "https://www.alternadudes.com/content/contentthumbs/54/56/15456-1x.jpg" {
		t.Errorf("thumbnail = %q", e.thumbnail)
	}
	if e.trailerURL != "/trailers/Test-Scene.html" {
		t.Errorf("trailerURL = %q, want /trailers/Test-Scene.html", e.trailerURL)
	}

	e2 := entries[1]
	if e2.id != "12345" {
		t.Errorf("id = %q, want 12345", e2.id)
	}
	if e2.trailerURL != "" {
		t.Errorf("trailerURL = %q, want empty", e2.trailerURL)
	}
	if e2.listURL != "https://www.alternadudes.com/categories/movies.html" {
		t.Errorf("listURL = %q", e2.listURL)
	}
}

// A card with no trailer anchor still has to carry a URL: the listing page it
// appeared on is the only place the scene is reachable.
func TestProcessEntryWithoutTrailerFallsBackToListingPage(t *testing.T) {
	s := New()
	entry := listEntry{
		id:      "12345",
		title:   "No Trailer",
		listURL: "https://www.alternadudes.com/categories/movies_3_d.html",
	}
	scene := s.processEntry(context.Background(), "https://www.alternadudes.com", "https://www.alternadudes.com/", entry)
	if scene.URL != "https://www.alternadudes.com/categories/movies_3_d.html#12345" {
		t.Errorf("URL = %q", scene.URL)
	}
}

func TestEstimateTotal(t *testing.T) {
	body := []byte(`<a href="movies_2_d.html">2</a><a href="movies_39_d.html">39</a>`)
	if got := estimateTotal(body, 12); got != 468 {
		t.Errorf("estimateTotal = %d, want 468", got)
	}
}

func TestPageURL(t *testing.T) {
	base := "https://www.alternadudes.com"
	if got := pageURL(base, 1); got != "https://www.alternadudes.com/categories/movies.html" {
		t.Errorf("page 1 = %q", got)
	}
	if got := pageURL(base, 5); got != "https://www.alternadudes.com/categories/movies_5_d.html" {
		t.Errorf("page 5 = %q", got)
	}
}

const detailPage = `<html><head>
<meta name="keywords" content="Balls,Blonde,Dirty Talk,Thick Cock,Christop">
<meta property="og:image" content="https://www.alternadudes.com/content/test/0.jpg">
</head><body>
<h4>A great scene description here.</h4>
</body></html>`

var testPageNumRe = regexp.MustCompile(`movies_(\d+)_d\.html`)

func newTestServer(pages [][]string, detail string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")

		switch r.URL.Path {
		case "/trailers/Test-Scene.html":
			_, _ = fmt.Fprint(w, detail)

		default:
			pageNum := 1
			if m := testPageNumRe.FindStringSubmatch(r.URL.Path); m != nil {
				pageNum, _ = strconv.Atoi(m[1])
			}
			idx := pageNum - 1
			if idx >= 0 && idx < len(pages) {
				pager := ""
				for p := 2; p <= len(pages); p++ {
					pager += fmt.Sprintf(`<a href="movies_%d_d.html">%d</a>`, p, p)
				}
				var cards string
				for _, id := range pages[idx] {
					cards += fmt.Sprintf(`<div class="item col-lg-4 col-md-4 col-12 padx">
  <div class="product-item">
    <div class="pi-img-wrapper">
      <a href="/trailers/Test-Scene.html">
        <img src="/content/contentthumbs/00/00/%s-1x.jpg" alt="Scene %s" class="img-fluid">
      </a>
    </div>
  </div>
</div>`, id, id)
				}
				_, _ = fmt.Fprint(w, pager+cards)
			}
		}
	}))
}

func TestRun(t *testing.T) {
	ts := newTestServer([][]string{{"100", "200"}}, detailPage)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Workers: 1,
		Delay:   time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2", len(got))
	}
	if got[0].SiteID != "alternadudes" {
		t.Errorf("siteID = %q", got[0].SiteID)
	}
}

func TestKnownIDs(t *testing.T) {
	ts := newTestServer([][]string{{"1", "2", "3"}}, detailPage)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"2": true},
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

func TestModelPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/models/SomeModel.html":
			cards := ""
			for _, id := range []string{"500", "600"} {
				cards += fmt.Sprintf(`<div class="item col-lg-4 col-md-4 col-12 padx">
  <div class="product-item">
    <div class="pi-img-wrapper">
      <a href="/trailers/Test-Scene.html">
        <img src="/content/contentthumbs/00/00/%s-1x.jpg" alt="Scene %s" class="img-fluid">
      </a>
    </div>
  </div>
</div>`, id, id)
			}
			_, _ = fmt.Fprint(w, cards)
		case "/trailers/Test-Scene.html":
			_, _ = fmt.Fprint(w, detailPage)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	modelURL := ts.URL + "/models/SomeModel.html"
	ch, err := s.ListScenes(context.Background(), modelURL, scraper.ListOpts{
		Workers: 1,
		Delay:   time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2", len(got))
	}
	for _, sc := range got {
		if sc.StudioURL != modelURL {
			t.Errorf("StudioURL = %q, want %q", sc.StudioURL, modelURL)
		}
	}
}

func TestPagination(t *testing.T) {
	ts := newTestServer([][]string{{"10", "20"}, {"30"}}, detailPage)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Workers: 1,
		Delay:   time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 3 {
		t.Fatalf("got %d scenes, want 3", len(got))
	}
}
