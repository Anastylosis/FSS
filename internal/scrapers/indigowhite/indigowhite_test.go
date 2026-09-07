package indigowhite

import (
	"context"
	"encoding/json"
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
		{"https://indigowhitetv.com/", true},
		{"https://www.indigowhitetv.com/freevideoshub", true},
		{"https://indigowhitetv.com/videos", true},
		{"https://indigowhitetv.com.evil.org/", false},
		{"https://indigowhite.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestCollectionFor(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"https://indigowhitetv.com/", collections},
		{"https://indigowhitetv.com/home", collections},
		{"https://indigowhitetv.com/videos", []string{"/videos"}},
		{"https://indigowhitetv.com/freevideoshub/", []string{"/freevideoshub"}},
	}
	for _, c := range cases {
		if got := collectionFor(c.in); !slices.Equal(got, c.want) {
			t.Errorf("collectionFor(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestToScene(t *testing.T) {
	s := New()
	it := apiItem{
		ID:       "6980eac01eb9f651408d961a",
		Title:    "Nurse Incubi",
		FullURL:  "/freevideoshub/nurse-incubi",
		AssetURL: "https://images.squarespace-cdn.com/content/v1/abc/thumb.jpg",
		// The body opens with the collection's whole category nav.
		Body:    `<div class="sqs-layout"><a>Anal</a><a>Cosplay</a><p>Incubus Nurse is stuck working.</p></div>`,
		Excerpt: `<p>Incubus Nurse is stuck working at the hospital... &amp;lt;3</p>`,
		Tags:    []string{"JANUARY", "2026", "SFW", "SFW"},
		// Squarespace timestamps are milliseconds since the epoch.
		Categories: []string{"BTS", "Wholesome"},
		PublishOn:  1770057699501,
	}
	sc := s.toScene(it, "https://indigowhitetv.com/")

	if sc.ID != it.ID || sc.SiteID != siteID {
		t.Errorf("id/site = %q/%q", sc.ID, sc.SiteID)
	}
	if sc.URL != "https://indigowhitetv.com/freevideoshub/nurse-incubi" {
		t.Errorf("url = %q", sc.URL)
	}
	if sc.Date.Format("2006-01-02") != "2026-02-02" {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Description != "Incubus Nurse is stuck working at the hospital... <3" {
		t.Errorf("description = %q — the excerpt is preferred over the nav-laden body", sc.Description)
	}
	if !slices.Equal(sc.Tags, []string{"JANUARY", "2026", "SFW"}) {
		t.Errorf("tags = %v — a repeat must not be stored twice", sc.Tags)
	}
	if !slices.Equal(sc.Categories, []string{"BTS", "Wholesome"}) {
		t.Errorf("categories = %v", sc.Categories)
	}
	if sc.Thumbnail != it.AssetURL {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
}

func TestToSceneFallsBackToTheBody(t *testing.T) {
	s := New()
	sc := s.toScene(apiItem{ID: "x", Title: "T", Body: `<p>Only the body has copy.</p>`}, "https://indigowhitetv.com/")
	if sc.Description != "Only the body has copy." {
		t.Errorf("description = %q", sc.Description)
	}
	if !sc.Date.IsZero() {
		t.Errorf("date = %v, want zero when publishOn is absent", sc.Date)
	}
}

// ---- end-to-end ----

type fakeSite struct {
	// pages maps a collection to the number of cursor pages it serves.
	pages map[string]int
	per   int
	// echo makes the server hand back a cursor it already gave, which would
	// loop a walk that trusted nextPage alone.
	echo bool

	mu   sync.Mutex
	hits []string
}

func (f *fakeSite) hit(u string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits = append(f.hits, u)
}

func (f *fakeSite) hitList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.hits)
}

func (f *fakeSite) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.hit(r.URL.String())
		total, ok := f.pages[r.URL.Path]
		if !ok {
			t.Errorf("unexpected collection %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		// The cursor is the page number in this fake; the real one is a
		// publish timestamp, which the walk never interprets.
		page := 1
		if off := r.URL.Query().Get("offset"); off != "" {
			n, _ := strconv.Atoi(off)
			page = n
		}

		var out apiPage
		for i := range f.per {
			id := fmt.Sprintf("%s-%d-%d", strings.Trim(r.URL.Path, "/"), page, i)
			out.Items = append(out.Items, apiItem{
				ID: id, Title: "Scene " + id, FullURL: r.URL.Path + "/" + id,
				PublishOn: 1770057699501,
			})
		}
		switch {
		case f.echo:
			out.Pagination.NextPage = true
			out.Pagination.NextPageOffset = 2
		case page < total:
			out.Pagination.NextPage = true
			out.Pagination.NextPageOffset = int64(page + 1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func newTestScraper(t *testing.T, f *fakeSite) *Scraper {
	t.Helper()
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	return &Scraper{Client: ts.Client(), base: ts.URL}
}

func collect(t *testing.T, s *Scraper, studioURL string) ([]string, []error) {
	t.Helper()
	out := make(chan scraper.SceneResult, 2000)
	go s.run(context.Background(), studioURL, scraper.ListOpts{}, out)
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

func TestRunWalksBothCollections(t *testing.T) {
	f := &fakeSite{pages: map[string]int{"/freevideoshub": 3, "/videos": 1}, per: 2}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://indigowhitetv.com/")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(ids) != 3*2+1*2 {
		t.Errorf("got %d scenes, want 8", len(ids))
	}
	var sawVideos bool
	for _, u := range f.hitList() {
		if strings.HasPrefix(u, "/videos") {
			sawVideos = true
		}
	}
	if !sawVideos {
		t.Errorf("never asked for the second collection: %v", f.hitList())
	}
}

func TestRunOneCollectionFromItsURL(t *testing.T) {
	f := &fakeSite{pages: map[string]int{"/videos": 1}, per: 5}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, s.base+"/videos")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(ids) != 5 {
		t.Errorf("got %d scenes, want 5", len(ids))
	}
	for _, u := range f.hitList() {
		if strings.HasPrefix(u, "/freevideoshub") {
			t.Errorf("walked the other collection too: %v", f.hitList())
			break
		}
	}
}

// A server that hands back a cursor the walk already followed is echoing a
// page; trusting nextPage alone would loop until the agent cap.
func TestRunStopsOnARepeatedCursor(t *testing.T) {
	f := &fakeSite{pages: map[string]int{"/freevideoshub": 99, "/videos": 0}, per: 1, echo: true}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://indigowhitetv.com/")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(ids) > 4 {
		t.Errorf("emitted %d scenes from an echoing server, want a handful", len(ids))
	}
	if n := len(f.hitList()); n > 6 {
		t.Errorf("made %d requests against an echoing server", n)
	}
}

func TestRunReportsAnEmptyFirstCollection(t *testing.T) {
	f := &fakeSite{pages: map[string]int{"/freevideoshub": 1, "/videos": 1}, per: 0}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://indigowhitetv.com/")
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
