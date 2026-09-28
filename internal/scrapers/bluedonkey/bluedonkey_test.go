package bluedonkey

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestRegisteredSites(t *testing.T) {
	for _, id := range []string{"kimholland", "meidenvanholland", "vurigvlaanderen", "secretcircle"} {
		if newFor(id) == nil {
			t.Errorf("newFor(%q) returned nil", id)
		}
	}
	if newFor("nope") != nil {
		t.Errorf("newFor(unknown) should be nil")
	}
}

func TestMatchesURL(t *testing.T) {
	kh := newFor("kimholland")
	if !kh.MatchesURL("https://www.kimholland.com/videos/") {
		t.Errorf("kimholland should match its own URL")
	}
	if kh.MatchesURL("https://meidenvanholland.nl/sexfilms") {
		t.Errorf("kimholland should not match meidenvanholland")
	}
	mvh := newFor("meidenvanholland")
	if !mvh.MatchesURL("https://meidenvanholland.nl/sexfilms") {
		t.Errorf("meidenvanholland should match its own URL")
	}
}

func TestParseKHListing(t *testing.T) {
	s := newFor("kimholland")
	items := s.parseKHListing(loadFixture(t, "kimholland_list.html"))
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	first := items[0]
	if first.id != "7297" {
		t.Errorf("id = %q, want 7297", first.id)
	}
	if first.url != "https://www.kimholland.com/video/7297" {
		t.Errorf("url = %q", first.url)
	}
	if !strings.Contains(first.title, "'") || !strings.HasPrefix(first.title, "Give me some pleasure") {
		t.Errorf("title not unescaped/parsed: %q", first.title)
	}
	if first.thumbnail != "https://www.kimholland.com/images/7297/1/550x309.jpg" {
		t.Errorf("thumbnail = %q", first.thumbnail)
	}
}

func TestParseKHDetail(t *testing.T) {
	it := listItem{id: "7275"}
	parseKHDetail(loadFixture(t, "kimholland_detail.html"), &it)
	if it.title != "Dick addicted dirty talking MILF Sandra" {
		t.Errorf("title = %q", it.title)
	}
	if !strings.HasPrefix(it.desc, "It's that time again") {
		t.Errorf("desc = %q", it.desc)
	}
	if it.thumbnail != "/images/7275/1920x1080.jpg" {
		t.Errorf("thumbnail = %q", it.thumbnail)
	}
}

func TestParseSyseroListing(t *testing.T) {
	s := newFor("meidenvanholland")
	items := s.parseSyseroListing(loadFixture(t, "meidenvanholland_list.html"))
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	first := items[0]
	if first.id == "" {
		t.Errorf("first id empty")
	}
	if !strings.HasPrefix(first.url, "https://meidenvanholland.nl/sexfilms/") {
		t.Errorf("url = %q", first.url)
	}
	if first.title == "" {
		t.Errorf("title empty")
	}
	if first.duration <= 0 {
		t.Errorf("duration = %d, want > 0", first.duration)
	}
	if first.thumbnail == "" {
		t.Errorf("thumbnail empty")
	}
}

func TestParseSyseroListingSecretCircle(t *testing.T) {
	// Secret Circle cards are minimal: slug + poster title only, no numeric id.
	s := newFor("secretcircle")
	items := s.parseSyseroListing(loadFixture(t, "secretcircle_list.html"))
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	first := items[0]
	if first.id != "de-masseuse-en-lucas" {
		t.Errorf("id = %q, want slug fallback", first.id)
	}
	if first.title != "De masseuse en Lucas" {
		t.Errorf("title = %q (from poster attr)", first.title)
	}
	if first.url != "https://secretcircle.com/seksfilms/de-masseuse-en-lucas" {
		t.Errorf("url = %q", first.url)
	}
}

func TestParseSyseroDetail(t *testing.T) {
	it := listItem{id: "x"}
	parseSyseroDetail(loadFixture(t, "meidenvanholland_detail.html"), &it)
	if it.title != "Sex in de barbershop" {
		t.Errorf("title = %q", it.title)
	}
	if !strings.HasPrefix(it.desc, "Daphne is de geile kapster") {
		t.Errorf("desc = %q", it.desc)
	}
	// Performers derived from /modellen/ slugs, deduped.
	if len(it.performers) != 2 {
		t.Errorf("performers = %v, want 2", it.performers)
	}
	if it.performers[0] != "Daphne Laat" || it.performers[1] != "Cj Bangz" {
		t.Errorf("performers = %v", it.performers)
	}
	// Tags derived from /genres/ slugs, deduped.
	if len(it.tags) == 0 {
		t.Errorf("tags empty")
	}
	hasMilf := false
	for _, tg := range it.tags {
		if tg == "Milf" {
			hasMilf = true
		}
	}
	if !hasMilf {
		t.Errorf("tags = %v, want a Milf tag", it.tags)
	}
}

func TestRunKimHollandEndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/videos", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write(loadFixture(t, "kimholland_list_empty.html"))
			return
		}
		_, _ = w.Write(loadFixture(t, "kimholland_list.html"))
	})
	mux.HandleFunc("/video/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(loadFixture(t, "kimholland_detail.html"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	s := newFor("kimholland")
	s.base = ts.URL
	s.Client = ts.Client()

	scenes := collect(t, s)
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.SiteID != "kimholland" || sc.Studio != "Kim Holland" {
		t.Errorf("site/studio = %q/%q", sc.SiteID, sc.Studio)
	}
	// Detail page overrides title/desc/thumbnail for every scene.
	if sc.Title != "Dick addicted dirty talking MILF Sandra" {
		t.Errorf("title = %q", sc.Title)
	}
	if !strings.HasPrefix(sc.Description, "It's that time again") {
		t.Errorf("desc = %q", sc.Description)
	}
	if sc.Thumbnail != "/images/7275/1920x1080.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if sc.URL != ts.URL+"/video/7297" {
		t.Errorf("url = %q", sc.URL)
	}
}

func TestRunSyseroEndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sexfilms", func(w http.ResponseWriter, r *http.Request) {
		if strings.Count(r.URL.Path, "/") > 1 { // detail path /sexfilms/{slug}
			_, _ = w.Write(loadFixture(t, "meidenvanholland_detail.html"))
			return
		}
		_, _ = w.Write(loadFixture(t, "meidenvanholland_list.html"))
	})
	mux.HandleFunc("/sexfilms/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(loadFixture(t, "meidenvanholland_detail.html"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	s := newFor("meidenvanholland")
	s.base = ts.URL
	s.Client = ts.Client()

	scenes := collect(t, s)
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.SiteID != "meidenvanholland" || sc.Studio != "Meiden van Holland" {
		t.Errorf("site/studio = %q/%q", sc.SiteID, sc.Studio)
	}
	// Detail enrichment supplies VideoObject title/desc + performers/tags.
	if sc.Title != "Sex in de barbershop" {
		t.Errorf("title = %q", sc.Title)
	}
	if len(sc.Performers) != 2 {
		t.Errorf("performers = %v", sc.Performers)
	}
	if len(sc.Tags) == 0 {
		t.Errorf("tags empty")
	}
	if sc.Duration <= 0 {
		t.Errorf("duration from listing card lost: %d", sc.Duration)
	}
}

func TestKnownIDEarlyStop(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/videos", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(loadFixture(t, "kimholland_list.html"))
	})
	mux.HandleFunc("/video/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(loadFixture(t, "kimholland_detail.html"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	s := newFor("kimholland")
	s.base = ts.URL
	s.Client = ts.Client()

	out, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{
		KnownIDs: map[string]bool{"7296": true}, // second card
	})
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	stopped := false
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindStoppedEarly:
			stopped = true
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if !stopped {
		t.Errorf("expected StoppedEarly on known ID")
	}
	if len(scenes) != 2 {
		t.Errorf("got %d scenes before stop, want 2 — the page is finished first", len(scenes))
	}
}

func collect(t *testing.T, s *Scraper) []models.Scene {
	t.Helper()
	out, err := s.ListScenes(context.Background(), s.base, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	return scenes
}
