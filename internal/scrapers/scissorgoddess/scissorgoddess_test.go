package scissorgoddess

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func readFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/products.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return b
}

func fixtureProducts(t *testing.T) []wpProduct {
	t.Helper()
	var ps []wpProduct
	if err := json.Unmarshal(readFixture(t), &ps); err != nil {
		t.Fatalf("decoding fixture: %v", err)
	}
	return ps
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://scissorgoddess.net", true},
		{"https://scissorgoddess.net/", true},
		{"https://www.scissorgoddess.net/product/the-session/", true},
		{"https://scissorgoddess.com/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

func TestID(t *testing.T) {
	if got := New().ID(); got != siteID {
		t.Errorf("ID() = %q, want %q", got, siteID)
	}
}

func TestToScene(t *testing.T) {
	ps := fixtureProducts(t)
	if len(ps) == 0 {
		t.Fatal("fixture is empty")
	}
	now := time.Now().UTC()

	sc := toScene("https://scissorgoddess.net", ps[0], now)

	if sc.ID != strconv.Itoa(ps[0].ID) {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("SiteID = %q, Studio = %q", sc.SiteID, sc.Studio)
	}
	if sc.Title == "" {
		t.Error("Title is empty")
	}
	// WP renders typographic entities into titles; they must be decoded.
	if strings.Contains(sc.Title, "&#") || strings.Contains(sc.Title, "&amp;") {
		t.Errorf("Title still holds HTML entities: %q", sc.Title)
	}
	if sc.Description == "" {
		t.Error("Description is empty")
	}
	if strings.Contains(sc.Description, "<") {
		t.Errorf("Description still holds markup: %q", sc.Description)
	}
	if sc.Date.IsZero() {
		t.Error("Date is zero")
	}
	if sc.URL == "" {
		t.Error("URL is empty")
	}
	if !sc.ScrapedAt.Equal(now) {
		t.Errorf("ScrapedAt = %v", sc.ScrapedAt)
	}
}

// Only three of the embedded taxonomies are scene metadata. product_cat is the
// storefront section ("Video") and product_brand the publisher — neither may
// leak into performers, categories or tags.
func TestToSceneTaxonomyMapping(t *testing.T) {
	p := wpProduct{ID: 1, Title: wpRendered{Rendered: "T"}}
	p.Embedded.Terms = [][]wpTerm{
		{{Name: "Media Solutions Inc", Taxonomy: "product_brand"}},
		{{Name: "Video", Taxonomy: "product_cat"}},
		{{Name: "anal", Taxonomy: "product_tag"}, {Name: "femdom", Taxonomy: "product_tag"}},
		{{Name: "Goddess Rapture", Taxonomy: "model"}},
		{{Name: "Female Domination", Taxonomy: "genre"}, {Name: "Pegging", Taxonomy: "genre"}},
	}

	sc := toScene("https://scissorgoddess.net", p, time.Now())

	if !slices.Equal(sc.Performers, []string{"Goddess Rapture"}) {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if !slices.Equal(sc.Categories, []string{"Female Domination", "Pegging"}) {
		t.Errorf("Categories = %v", sc.Categories)
	}
	if !slices.Equal(sc.Tags, []string{"anal", "femdom"}) {
		t.Errorf("Tags = %v", sc.Tags)
	}
	for _, unwanted := range []string{"Video", "Media Solutions Inc"} {
		if slices.Contains(sc.Tags, unwanted) ||
			slices.Contains(sc.Categories, unwanted) ||
			slices.Contains(sc.Performers, unwanted) {
			t.Errorf("storefront term %q leaked into scene metadata: %+v", unwanted, sc)
		}
	}
}

func TestToSceneUsesFeaturedMedia(t *testing.T) {
	p := wpProduct{ID: 2, Title: wpRendered{Rendered: "T"}}
	p.Embedded.FeaturedMedia = []struct {
		SourceURL string `json:"source_url"`
	}{{SourceURL: "https://scissorgoddess.net/wp-content/uploads/a.jpg"}}

	if got := toScene("x", p, time.Now()).Thumbnail; got != "https://scissorgoddess.net/wp-content/uploads/a.jpg" {
		t.Errorf("Thumbnail = %q", got)
	}
}

func TestCleanText(t *testing.T) {
	cases := map[string]string{
		"The Session &#8211; Rapture":    "The Session – Rapture",
		"Pegging &#038; Humiliation":     "Pegging & Humiliation",
		"<p>Some  text</p>\n<p>more</p>": "<p>Some text</p> <p>more</p>",
		"":                               "",
	}
	for in, want := range cases {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- end-to-end ----

// newTestServer serves `total` products at perPage per page and answers HTTP
// 400 past the last page, the way WordPress does.
func newTestServer(t *testing.T, total int) *httptest.Server {
	t.Helper()
	tmpl := fixtureProducts(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wp-json/wp/v2/product" {
			http.NotFound(w, r)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		totalPages := (total + perPage - 1) / perPage
		if page > totalPages {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"code":"rest_post_invalid_page_number"}`)
			return
		}

		start := (page - 1) * perPage
		end := min(start+perPage, total)
		out := make([]wpProduct, 0, end-start)
		for i := start; i < end; i++ {
			p := tmpl[i%len(tmpl)]
			p.ID = 10000 + i
			out = append(out, p)
		}

		w.Header().Set("X-WP-Total", strconv.Itoa(total))
		w.Header().Set("X-WP-TotalPages", strconv.Itoa(totalPages))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func run(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	orig := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = orig })

	s := New()
	s.Client = srv.Client()

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, sc := range testutil.CollectScenes(t, ch) {
		ids = append(ids, sc.ID)
	}
	return ids
}

func TestListScenes(t *testing.T) {
	ids := run(t, newTestServer(t, 250))
	if len(ids) != 250 {
		t.Fatalf("got %d scenes, want 250", len(ids))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate scene ID %q", id)
		}
		seen[id] = true
	}
}

// At an exact multiple of the page size the loop asks for one page past the
// end and WP answers 400. That must end the walk, not fail the run.
func TestListScenesExactPageMultiple(t *testing.T) {
	if ids := run(t, newTestServer(t, 200)); len(ids) != 200 {
		t.Fatalf("got %d scenes, want 200", len(ids))
	}
}

func TestListScenesSinglePage(t *testing.T) {
	if ids := run(t, newTestServer(t, 2)); len(ids) != 2 {
		t.Fatalf("got %d scenes, want 2", len(ids))
	}
}

// A first-page failure is a real error, not the end of the listing.
func TestFirstPageErrorIsReported(t *testing.T) {
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
		t.Error("a page-1 failure produced no error result")
	}
}

// This pins the **wire format**, not the scraper's use of it. Changing toScene's
// layout is already caught by TestToScene via Date.IsZero(); what nothing covered
// is the *site* changing format, and the two failures look identical from
// TestToScene alone.
//
// WordPress's date_gmt carries no timezone suffix ("2025-03-02T23:03:28"), so it
// deliberately does not parse as RFC3339 and toScene uses an explicit
// "2006-01-02T15:04:05" layout. If WP starts emitting an offset, this test says so
// directly instead of leaving a zero Date to be diagnosed as a code bug — and a
// zero Date is only an advisory note at validation time, so a whole catalogue can
// scrape dateless with the smoke run still passing.
func TestDateGMTLayout(t *testing.T) {
	ps := fixtureProducts(t)
	if len(ps) == 0 {
		t.Fatal("fixture is empty")
	}
	raw := ps[0].DateGMT
	if raw == "" {
		t.Fatal("DateGMT is empty (date_gmt)")
	}
	if _, err := time.Parse(time.RFC3339, raw); err == nil {
		t.Errorf("date_gmt %q parsed as RFC3339 — the field gained a timezone suffix, so the "+
			"explicit layout in toScene is now the wrong one", raw)
	}
	if _, err := time.Parse("2006-01-02T15:04:05", raw); err != nil {
		t.Errorf("date_gmt %q does not parse with the layout toScene uses: %v", raw, err)
	}
}

// N-A / NL1: a page-2+ failure that is *not* WP's past-the-end 400 must surface as an
// error, not be reported as end-of-listing.
//
// The distinction is what makes this severe rather than cosmetic. Under `--full` the
// store treats the returned scenes as the studio's complete state and hard-deletes
// everything the run did not reach — price history included. Reporting Done on a 502
// therefore turns one transient blip into permanent data loss, with a success line
// printed. Only HTTP 400 past page 1 means the listing ended.
//
// Page 1 must return a *full* page (perPage products) or the walk stops there on
// `len(products) < perPage` and never reaches the failing page — which is exactly what
// a first draft of this test did, passing the 400 case while proving nothing.
func TestPageErrorPastFirstPageIsReported(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantErrors bool
	}{
		{"400 is WP's past-the-end marker", http.StatusBadRequest, false},
		{"502 is a real failure", http.StatusBadGateway, true},
		{"429 is a real failure", http.StatusTooManyRequests, true},
		{"500 is a real failure", http.StatusInternalServerError, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpl := fixtureProducts(t)
			var page2Hits atomic.Int32

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if page < 1 {
					page = 1
				}
				if page > 1 {
					page2Hits.Add(1)
					w.WriteHeader(c.status)
					return
				}
				// A full page, so the walk continues past it.
				out := make([]wpProduct, 0, perPage)
				for i := 0; i < perPage; i++ {
					p := tmpl[i%len(tmpl)]
					p.ID = 100000 + i
					out = append(out, p)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer ts.Close()

			orig := siteBase
			siteBase = ts.URL
			t.Cleanup(func() { siteBase = orig })

			s := New()
			s.Client = ts.Client()

			ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
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
				}
			}
			if scenes != perPage {
				t.Fatalf("page 1 produced %d scenes, want %d — the walk must reach page 2", scenes, perPage)
			}
			if page2Hits.Load() == 0 {
				t.Fatal("page 2 was never requested; the error path was not exercised")
			}
			if c.wantErrors && errs == 0 {
				t.Errorf("HTTP %d on page 2 produced no scraper.Error — the run looks complete, "+
					"and --full would delete every scene past page 1", c.status)
			}
			if !c.wantErrors && errs != 0 {
				t.Errorf("HTTP %d on page 2 produced %d error(s); it is WP's end-of-listing marker",
					c.status, errs)
			}
		})
	}
}

// The 400 past the end is the only page>1 failure that means end-of-listing.
// Reporting Done for any of them made a WAF block or DNS blip on page 4 of 12
// indistinguishable from the end, and `--full` then deleted pages 4-12 while
// printing a success line.
func TestLatePageFailureIsAnErrorNotTheEnd(t *testing.T) {
	for _, tt := range []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"past the end", http.StatusBadRequest, false},
		{"rate limited", http.StatusTooManyRequests, true},
		{"bad gateway", http.StatusBadGateway, true},
		{"forbidden", http.StatusForbidden, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if page > 1 {
					w.WriteHeader(tt.status)
					return
				}
				out := make([]map[string]any, 0, perPage)
				for i := 0; i < perPage; i++ {
					out = append(out, map[string]any{
						"id": 1000 + i, "date": "2026-01-01T00:00:00",
						"link":  srvLink(r, i),
						"title": map[string]string{"rendered": "Scene"},
					})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(out)
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
			var errs int
			for r := range ch {
				if r.Kind == scraper.KindError {
					errs++
				}
			}
			if tt.wantErr && errs == 0 {
				t.Errorf("HTTP %d on page 2 emitted no error — the run would look complete", tt.status)
			}
			if !tt.wantErr && errs != 0 {
				t.Errorf("HTTP %d on page 2 is the end of the listing, not an error", tt.status)
			}
		})
	}
}

func srvLink(r *http.Request, i int) string {
	return "https://" + r.Host + "/product/scene-" + strconv.Itoa(i) + "/"
}
