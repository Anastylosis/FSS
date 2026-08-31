package shopmaker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://kinkymistresses.com/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "kinkymistresses" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestMatchesOwnDomainOnly(t *testing.T) {
	s := newFor("kinkymistresses")
	for _, u := range []string{"https://kinkymistresses.com/", "https://www.kinkymistresses.com/videos", "http://kinkymistresses.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://kinkymistresses.net/", "https://notkinkymistresses.com/", "https://example.com/kinkymistresses.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

// The apex 301s to www and drops the path doing it, so a scrape rooted at the
// apex would fetch page 1 over and over.
func TestBaseIsTheWWWHost(t *testing.T) {
	if got := newFor("kinkymistresses").base; got != "https://www.kinkymistresses.com" {
		t.Errorf("base = %q", got)
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing_page1.html"))
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	if first.id == "" {
		t.Error("id missing")
	}
	if !strings.HasPrefix(first.path, "/collections/") {
		t.Errorf("path = %q", first.path)
	}
	if first.title == "" {
		t.Error("title missing")
	}
	if first.date == "" {
		t.Errorf("date = %q", first.date)
	}
	if first.duration == 0 {
		t.Error("duration missing")
	}
	// The Models anchors carry a tooltip whose value is an escaped <img …/>
	// tag, so it contains a real ">" inside quotes. A plain `<a[^>]*>` left
	// markup in the name.
	for _, p := range first.performers {
		if strings.ContainsAny(p, "<>\"") {
			t.Errorf("performer %q still carries markup", p)
		}
	}
	if len(first.performers) == 0 {
		t.Error("performers missing")
	}
	if !strings.HasPrefix(first.thumbnail, "https://") {
		t.Errorf("thumbnail = %q", first.thumbnail)
	}
	if !strings.HasSuffix(strings.Split(first.preview, "?")[0], ".mp4") {
		t.Errorf("preview = %q", first.preview)
	}
}

// The meta strip labels the category span "Categories" on some cards and
// "Category" on others.
func TestParseListingAcceptsBothCategoryLabels(t *testing.T) {
	items := parseListing(readFixture(t, "listing_page1.html"))
	found := false
	for _, it := range items {
		if len(it.categories) > 0 {
			found = true
		}
	}
	if !found {
		t.Error("no card yielded a category")
	}

	singular := []byte(`<div class="card" id="collection_1">` +
		`<div class="meta-description"><span title="Category" class="x"><i></i><span class="fa5-text"><a href="/collections?category=Anal+Sex">Anal Sex</a></span></span> &minus; <span title="Production year"><span class="fa5-text">2025</span></span></div>` +
		`<h2 class="card-title h6"><a href="/collections/x">X</a></h2></div>`)
	got := parseListing(singular)
	if len(got) != 1 || len(got[0].categories) != 1 || got[0].categories[0] != "Anal Sex" {
		t.Errorf("singular label parsed as %+v", got)
	}
}

func TestParseDescription(t *testing.T) {
	got := parseDescription(readFixture(t, "detail.html"))
	if got == "" || strings.Contains(got, "&") {
		t.Errorf("description = %q", got)
	}
	if parseDescription([]byte("<html></html>")) != "" {
		t.Error("a page without og:description should yield empty")
	}
}

func TestToScene(t *testing.T) {
	s := newFor("kinkymistresses")
	item := listItem{
		id: "1062034128", path: "/collections/pegged", title: "Pegged",
		thumbnail: "https://images5.shopmaker.com/x.jpg", preview: "https://files5.shopmaker.com/x.mp4",
		performers: []string{"MISTRESS DAMAZONIA"}, categories: []string{"Anal Sex"},
		date: "2026-08-16", duration: 983, description: "D",
	}
	sc := s.toScene(item, "https://kinkymistresses.com/", time.Now().UTC())
	if sc.URL != "https://www.kinkymistresses.com/collections/pegged" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Date.Format("2006-01-02") != "2026-08-16" {
		t.Errorf("Date = %v", sc.Date)
	}
	// Prices are shown in the visitor's own currency, so none is recorded.
	if len(sc.PriceHistory) != 0 {
		t.Errorf("PriceHistory = %v, want none", sc.PriceHistory)
	}

	item.date = "not a date"
	if got := s.toScene(item, "https://kinkymistresses.com/", time.Now().UTC()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

func TestListScenes(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/collections/page/"):
			pages = append(pages, r.URL.Path)
			// Page 2 and beyond are empty: the pager is a sliding window, so
			// an empty page is the only end-of-listing signal.
			if strings.HasSuffix(r.URL.Path, "/1") {
				_, _ = w.Write(readFixture(t, "listing_page1.html"))
				return
			}
			_, _ = w.Write([]byte(`<html><body></body></html>`))
		case strings.HasPrefix(r.URL.Path, "/collections/"):
			_, _ = w.Write(readFixture(t, "detail.html"))
		default:
			t.Errorf("unexpected fetch %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	s := newFor("kinkymistresses")
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://kinkymistresses.com/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []models.Scene
	total := 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindTotal:
			total = res.Total
		case scraper.KindError:
			t.Errorf("error result: %v", res.Err)
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	if total != 2 {
		t.Errorf("Total = %d, want 2", total)
	}
	if len(pages) != 2 {
		t.Errorf("listing pages fetched = %v, want two", pages)
	}
	for _, sc := range scenes {
		if sc.Description == "" {
			t.Errorf("detail enrichment missing on %s", sc.ID)
		}
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/collections/page/") {
			_, _ = w.Write(readFixture(t, "listing_page1.html"))
			return
		}
		_, _ = w.Write(readFixture(t, "detail.html"))
	}))
	defer srv.Close()

	known := parseListing(readFixture(t, "listing_page1.html"))[1].id
	s := newFor("kinkymistresses")
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://kinkymistresses.com/",
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

func TestListScenesReportsDetailFailureButKeepsScene(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/page/1"):
			_, _ = w.Write(readFixture(t, "listing_page1.html"))
		case strings.HasPrefix(r.URL.Path, "/collections/page/"):
			_, _ = w.Write([]byte(`<html><body></body></html>`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	s := newFor("kinkymistresses")
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://kinkymistresses.com/", scraper.ListOpts{})
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
