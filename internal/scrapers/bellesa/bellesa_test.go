package bellesa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.bellesa.co/videos?providers=bellesa-films", true},
		{"https://bellesa.co/videos?providers=bellesa-blind-date", true},
		{"https://bellesaplus.co/videos?page=1&providers=bellesa-films", true},
		{"https://bellesaplus.co/studio-preview/bellesa-originals/videos?providers=zero-to-hero", true},
		{"https://www.bellesa.co/channels/bellesa-films", true},

		{"https://www.bellesa.co/videos", false},
		{"https://www.bellesa.co/videos/13250/the-bad-liar", false},
		{"https://www.bellesa.co/", false},
		{"https://notbellesa.co/videos?providers=bellesa-films", false},
		{"bellesa.co/videos?providers=bellesa-films", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestProviderHandle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://www.bellesa.co/videos?providers=Bellesa-Films", "bellesa-films"},
		{"https://www.bellesa.co/channels/bellesa+house+party", "bellesa-house-party"},
		{"https://www.bellesa.co/channels/bellesa%20house%20party", "bellesa-house-party"},
		{"https://bellesaplus.co/videos?providers=belle-says&page=3", "belle-says"},
		{"https://www.bellesa.co/videos?providers=", ""},
	}
	for _, c := range cases {
		if got := providerHandle(c.in); got != c.want {
			t.Errorf("providerHandle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPreferredStudioURL(t *testing.T) {
	s := New()
	want := "https://www.bellesa.co/videos?providers=bellesa-films"
	for _, in := range []string{
		"https://bellesaplus.co/studio-preview/bellesa-originals/videos?providers=bellesa-films",
		"https://bellesa.co/videos?page=1&providers=bellesa-films",
		"https://www.bellesa.co/channels/bellesa-films",
	} {
		if got := s.PreferredStudioURL(in); got != want {
			t.Errorf("PreferredStudioURL(%q) = %q, want %q", in, got, want)
		}
	}
	if got := s.PreferredStudioURL("https://example.com/videos?providers=x"); got != "" {
		t.Errorf("PreferredStudioURL(other host) = %q, want empty", got)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"The Bad Liar", "the-bad-liar"},
		{"We Can't", "we-cant"},
		{"You're A Model Today", "youre-a-model-today"},
		{"The Girl I Can't Have", "the-girl-i-cant-have"},
		{"Hot & Bothered!", "hot-bothered"},
		{"", ""},
	}
	for _, c := range cases {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSceneURLFallsBackToIDOnly(t *testing.T) {
	if got := sceneURL(42, "!!!"); got != "https://www.bellesa.co/videos/42" {
		t.Errorf("sceneURL = %q", got)
	}
}

func TestTopResolution(t *testing.T) {
	cases := []struct{ in, want string }{
		{"360,480,720,1080", "1080"},
		{"360", "360"},
		{"", ""},
		{"junk", ""},
	}
	for _, c := range cases {
		if got := topResolution(c.in); got != c.want {
			t.Errorf("topResolution(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitTags(t *testing.T) {
	// The site repeats a tag in both spellings; only the first is kept.
	got := splitTags("bellesa films, Bellesa Films ,story,,story")
	want := []string{"bellesa films", "story"}
	if len(got) != len(want) {
		t.Fatalf("splitTags = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitTags = %v, want %v", got, want)
		}
	}
	if splitTags("") != nil {
		t.Error("empty tag string should yield nil")
	}
}

func TestParseInitialDataMissing(t *testing.T) {
	if _, err := parseInitialData([]byte("<html>nothing here</html>")); err == nil {
		t.Fatal("expected an error for a page with no state blob")
	}
}

func TestObjectEndSkipsBracesInStrings(t *testing.T) {
	in := []byte(`{"a":"}{","b":{"c":1}} trailing`)
	end, ok := objectEnd(in)
	if !ok || string(in[:end]) != `{"a":"}{","b":{"c":1}}` {
		t.Fatalf("objectEnd = %d %v -> %q", end, ok, in[:end])
	}
	if _, ok := objectEnd([]byte(`{"a":1`)); ok {
		t.Error("unterminated object should not parse")
	}
	if _, ok := objectEnd([]byte(`"a"`)); ok {
		t.Error("non-object should not parse")
	}
}

func TestListScenes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/videos" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("providers") != "bellesa-films" {
			t.Errorf("providers = %q", r.URL.Query().Get("providers"))
		}
		name := "listing_page1.html"
		if r.URL.Query().Get("page") == "2" {
			name = "listing_page2.html"
		}
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Errorf("fixture: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	studioURL := "https://www.bellesa.co/videos?providers=bellesa-films"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
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

	if total != 3 {
		t.Errorf("Total = %d, want 3", total)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}

	first := scenes[0]
	if first.ID != "13250" || first.SiteID != "bellesa" {
		t.Errorf("ID/SiteID = %q/%q", first.ID, first.SiteID)
	}
	if first.Title != "The Bad Liar" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.URL != "https://www.bellesa.co/videos/13250/the-bad-liar" {
		t.Errorf("URL = %q", first.URL)
	}
	if first.StudioURL != studioURL {
		t.Errorf("StudioURL = %q", first.StudioURL)
	}
	if first.Studio != "Bellesa Films" {
		t.Errorf("Studio = %q", first.Studio)
	}
	if first.Duration != 573 {
		t.Errorf("Duration = %d", first.Duration)
	}
	if first.Date.IsZero() || first.Date.Location() != time.UTC {
		t.Errorf("Date = %v", first.Date)
	}
	if len(first.Performers) == 0 {
		t.Error("expected performers")
	}
	if len(first.Categories) == 0 {
		t.Error("expected categories")
	}
	if first.ScrapedAt.IsZero() {
		t.Error("ScrapedAt not set")
	}

	last := scenes[2]
	if last.URL != "https://www.bellesa.co/videos/9001/youre-a-model-today" {
		t.Errorf("page-2 URL = %q", last.URL)
	}
	if last.Resolution != "1080" {
		t.Errorf("Resolution = %q", last.Resolution)
	}
	if last.Description != "A & B, with <b>bold</b> removed by nobody." {
		t.Errorf("Description = %q", last.Description)
	}
	if len(last.Performers) != 1 || last.Performers[0] != "Jane Doe" {
		t.Errorf("Performers = %v", last.Performers)
	}
}

func TestListScenesStopsAtLastPage(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		b, _ := os.ReadFile(filepath.Join("testdata", "listing_page2.html"))
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL
	ch, err := s.ListScenes(context.Background(), "https://www.bellesa.co/videos?providers=bellesa-films", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	for range ch {
	}
	// The fixture reports page 2 of 2, so the loop must not ask for a third.
	if pages != 1 {
		t.Errorf("fetched %d pages, want 1", pages)
	}
}

func TestListScenesRejectsURLWithoutProvider(t *testing.T) {
	if _, err := New().ListScenes(context.Background(), "https://www.bellesa.co/videos", scraper.ListOpts{}); err == nil {
		t.Fatal("expected an error for a URL naming no provider")
	}
}
