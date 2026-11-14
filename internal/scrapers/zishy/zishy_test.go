package zishy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

func TestID(t *testing.T) {
	if got := New().ID(); got != siteID {
		t.Errorf("ID() = %q, want %q", got, siteID)
	}
}

// The site's TLS stack offers nothing Go's default cipher list will negotiate,
// so a plain client cannot complete the handshake at all.
func TestUsesTheLegacyTLSClient(t *testing.T) {
	tr, ok := New().Client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport is %T, want *http.Transport", New().Client.Transport)
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("the default transport cannot reach zishy.com — the legacy TLS client is required")
	}
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("certificates must still be verified")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := map[string]bool{
		"https://www.zishy.com":                    true,
		"https://zishy.com/albums?page=2":          true,
		"https://zishy.com/albums/2718-mirra-jean": true,
		"https://zishy.com.evil.test/":             false,
		"":                                         false,
	}
	for u, want := range cases {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing.html"))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	it := items[0]
	if it.id != "2718" {
		t.Errorf("id = %q", it.id)
	}
	if it.title != "Mirra Jean In and Out of Jeans" {
		t.Errorf("title = %q — the first <strong> is the title, the second a photo count", it.title)
	}
	// Album links are relative without a leading slash.
	if want := siteBase + "/albums/2718-mirra-jean-in-and-out-of-jeans"; it.url != want {
		t.Errorf("url = %q, want %q", it.url, want)
	}
	if !strings.HasPrefix(it.thumb, siteBase+"/uploads/") {
		t.Errorf("thumb = %q — relative thumbs must be absolutised", it.thumb)
	}
	if want := time.Date(2026, time.June, 20, 0, 0, 0, 0, time.UTC); !it.date.Equal(want) {
		t.Errorf("date = %v, want %v", it.date, want)
	}
}

// Album hrefs carry no leading slash, so a naive join would produce
// "https://www.zishy.com" + "albums/…".
func TestRelativeLinksAreRebuiltAgainstTheBase(t *testing.T) {
	items := parseListing([]byte(`<div class='albumcover'>
	<a href="albums/2717-emma"><img src="/uploads/thumbs/x.jpg" /></a>
	<strong>Emma</strong>
	</div>`))
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if !strings.HasPrefix(items[0].url, siteBase+"/albums/") {
		t.Errorf("url = %q", items[0].url)
	}
	if !strings.HasPrefix(items[0].thumb, siteBase+"/uploads/") {
		t.Errorf("thumb = %q", items[0].thumb)
	}
}

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"albums/1-x":     siteBase + "/albums/1-x",
		"/uploads/a.jpg": siteBase + "/uploads/a.jpg",
		"//cdn/a.jpg":    "https://cdn/a.jpg",
		"https://x/a":    "https://x/a",
	}
	for in, want := range cases {
		if got := normalizeURL(in); got != want {
			t.Errorf("normalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- detail ----

func TestApplyDetail(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, string(readFixture(t, "detail.html")))

	if sc.Description == "" || strings.Contains(sc.Description, "<") {
		t.Errorf("Description = %q", sc.Description)
	}
	// The model is exposed only as a tag; its display name is the link text.
	if !slices.Equal(sc.Performers, []string{"Mirra Jean"}) {
		t.Errorf("Performers = %v", sc.Performers)
	}
}

// The detail page writes "added on" and its date across a newline, which a
// tight single-line pattern would miss.
func TestDetailDateSpansNewlines(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, "<span style='font-size:20px;'> added on \nJun 20, 2026 </span>")
	if want := time.Date(2026, time.June, 20, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
}

// The card's date is the simpler form, so it wins and the detail is only a
// fallback.
func TestCardDateWinsOverDetail(t *testing.T) {
	sc := models.Scene{Date: time.Date(2026, time.June, 20, 0, 0, 0, 0, time.UTC)}
	applyDetail(&sc, "added on \nJan 1, 1999")
	if sc.Date.Year() != 2026 {
		t.Errorf("Date = %v, want the card's value", sc.Date)
	}
}

// The tag anchor carries inline styling between the href and the text.
func TestTagLinkToleratesInlineStyling(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, `<a href="/albums?tag_id=798" style="color:#000; background-color: #d9cdef">#Mirra Jean</a>`)
	if !slices.Equal(sc.Performers, []string{"Mirra Jean"}) {
		t.Errorf("Performers = %v", sc.Performers)
	}
}

// ---- end-to-end ----

func newSiteServer(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	listing := readFixture(t, "listing.html")
	detail := readFixture(t, "detail.html")
	var listPages atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/albums/") {
			_, _ = w.Write(detail)
			return
		}
		if listPages.Add(1) == 1 {
			_, _ = w.Write(listing)
			return
		}
		_, _ = w.Write([]byte("<html><body>no albums</body></html>"))
	}))
	t.Cleanup(srv.Close)

	orig := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = orig })

	return srv, func() int { return int(listPages.Load()) }
}

func TestListScenes(t *testing.T) {
	srv, listPages := newSiteServer(t)
	s := New()
	s.Client = srv.Client()

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, sc := range scenes {
		if sc.SiteID != siteID || sc.Studio != studioName {
			t.Errorf("scene %s: SiteID=%q Studio=%q", sc.ID, sc.SiteID, sc.Studio)
		}
		if sc.Title == "" || sc.Date.IsZero() {
			t.Errorf("scene %s incomplete: %+v", sc.ID, sc)
		}
		// Zishy publishes photo albums; there is no video section at all.
		if sc.Duration != 0 {
			t.Errorf("scene %s: Duration = %d, want 0", sc.ID, sc.Duration)
		}
	}
	if got := listPages(); got != 2 {
		t.Errorf("fetched %d listing pages, want 2", got)
	}
}

func TestDetailFailureKeepsTheCard(t *testing.T) {
	listing := readFixture(t, "listing.html")
	var listPages atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/albums/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if listPages.Add(1) == 1 {
			_, _ = w.Write(listing)
			return
		}
		_, _ = w.Write([]byte("<html><body>no albums</body></html>"))
	}))
	defer srv.Close()

	orig := siteBase
	siteBase = srv.URL
	defer func() { siteBase = orig }()

	s := New()
	s.Client = srv.Client()

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	if scenes[0].Title == "" || scenes[0].Date.IsZero() {
		t.Errorf("card fields lost on detail failure: %+v", scenes[0])
	}
}

func TestContextCancellation(t *testing.T) {
	srv, _ := newSiteServer(t)
	s := New()
	s.Client = srv.Client()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range ch {
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("channel did not close after context cancellation")
	}
}

func TestListingErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	orig := siteBase
	siteBase = srv.URL
	defer func() { siteBase = orig }()

	s := New()
	s.Client = srv.Client()

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	sawErr := false
	for res := range ch {
		if res.Kind == scraper.KindError {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("a listing failure produced no error result")
	}
}

func TestParseListingSpanTitle(t *testing.T) {
	items := parseListing([]byte(`<div class='albumcover' style='margin:15px'>
<a href="albums/2785-rian-starber-took-my-photo-2"><img src="/uploads/t.jpg" /></a>
<br>
<span style='text-wrap:nowrap; line-height:24px'>Rian Starber Took My Photo 2</span>
<br>
<strong style='color:#888'>77 photos</strong>
 | Oct 17, 2026
</div>`))
	if len(items) != 1 || items[0].title != "Rian Starber Took My Photo 2" || items[0].date.IsZero() {
		t.Fatalf("got %+v", items)
	}
}
