package powershotz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// starRows mirrors what gsd_p.php returns for the fixture clips.
var starRows = []star{
	{ModelName: "Chrystyne", VidID: "19779401"},
	{ModelName: "Emily", VidID: "19779401"},
	{ModelName: "Aubry & Tatum", VidID: "12166033"},
	{ModelName: "Aubry & Tatum", VidID: "17195888"},
	{ModelName: "Ally2", VidID: "17195888"},
}

type fakeSite struct {
	srv        *httptest.Server
	starCalls  atomic.Int32
	streamHits atomic.Int32
}

func newFakeSite(t *testing.T) *fakeSite {
	t.Helper()
	clips, err := os.ReadFile("testdata/clips.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	photos, err := os.ReadFile("testdata/photos.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSite{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") == "" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/php/stream/gvop.php":
			f.streamHits.Add(1)
			switch body["st"] {
			case "clip":
				_, _ = w.Write(clips)
			case "photos":
				_, _ = w.Write(photos)
			}
		case "/php/gsd_p.php":
			f.starCalls.Add(1)
			var rows []star
			for _, s := range starRows {
				if s.VidID == body["p"] || s.ModelName == body["p"] {
					rows = append(rows, s)
				}
			}
			if rows == nil {
				rows = []star{}
			}
			_ = json.NewEncoder(w).Encode(rows)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSite) scraper() *Scraper {
	s := New()
	s.client = f.srv.Client()
	s.siteBase = f.srv.URL
	s.dataBase = f.srv.URL
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, []error, bool) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	var errs []error
	stopped := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs = append(errs, r.Err)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return scenes, errs, stopped
}

func byID(scenes []models.Scene) map[string]models.Scene {
	m := map[string]models.Scene{}
	for _, s := range scenes {
		m[s.ID] = s
	}
	return m
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://powershotz.com/",
		"https://powershotz.com",
		"https://www.powershotz.com/videos",
		"https://powershotz.com/Abbey",
		"https://powershotz.com/Aubry%20&%20Tatum",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{
		"https://powershopz.com/Powershotz/x",
		"https://example.com/powershotz.com",
		"https://powershotz.com/a/b",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestCatalogue(t *testing.T) {
	f := newFakeSite(t)
	s := f.scraper()
	studioURL := "https://powershotz.com/"
	scenes, errs, stopped := collect(t, s, studioURL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if stopped {
		t.Error("unexpected StoppedEarly without KnownIDs")
	}
	if len(scenes) != 5 {
		t.Fatalf("got %d scenes, want 5", len(scenes))
	}
	m := byID(scenes)

	sc := m["2646655"]
	if sc.Title != "18 in Stock" || sc.StudioURL != studioURL || sc.SiteID != siteID {
		t.Errorf("identity: %+v", sc)
	}
	if sc.URL != f.srv.URL+"/2646655" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Duration != 11*60 || sc.Format != "wmv" || sc.Studio != "Powershotz" {
		t.Errorf("duration/format/studio: %d %q %q", sc.Duration, sc.Format, sc.Studio)
	}
	if sc.Thumbnail != f.srv.URL+"/c4sImages/laynestockm.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.LowestPrice != 11.99 {
		t.Errorf("LowestPrice = %v", sc.LowestPrice)
	}
	if slices.Contains(sc.Tags, "18 in Stock") || !slices.Contains(sc.Tags, "BONDAGE") {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if len(sc.Performers) != 0 {
		t.Errorf("Performers = %v, want none", sc.Performers)
	}

	if got := m["19779401"].Performers; !slices.Equal(got, []string{"Chrystyne", "Emily"}) {
		t.Errorf("19779401 performers = %v", got)
	}
	if got := m["17195888"].Performers; !slices.Equal(got, []string{"Ally", "Aubry", "Tatum"}) {
		t.Errorf("17195888 performers = %v", got)
	}
	if d := m["22952847"].Description; !slices.Contains([]rune(d), '’') {
		t.Errorf("mojibake not repaired: %q", d)
	}
}

func TestCatalogueUsesModelLookupsForLargeBatches(t *testing.T) {
	f := newFakeSite(t)
	s := f.scraper()
	// Five new clips, four models: per-model lookups are cheaper.
	_, errs, _ := collect(t, s, "https://powershotz.com/", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := f.starCalls.Load(); got != 4 {
		t.Errorf("star lookups = %d, want 4 (one per model)", got)
	}
}

func TestKnownIDsSkipsStoredClips(t *testing.T) {
	f := newFakeSite(t)
	s := f.scraper()
	known := map[string]bool{"2646655": true, "12166033": true, "17195888": true, "22952847": true}
	scenes, errs, stopped := collect(t, s, "https://powershotz.com/", scraper.ListOpts{KnownIDs: known})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(scenes) != 1 || scenes[0].ID != "19779401" {
		t.Fatalf("scenes = %v", scenes)
	}
	if !stopped {
		t.Error("want StoppedEarly when stored clips were skipped")
	}
	if got := f.starCalls.Load(); got != 1 {
		t.Errorf("star lookups = %d, want 1 (per clip)", got)
	}

	// Nothing new: no performer lookups at all.
	f.starCalls.Store(0)
	known["19779401"] = true
	scenes, _, _ = collect(t, s, "https://powershotz.com/", scraper.ListOpts{KnownIDs: known})
	if len(scenes) != 0 || f.starCalls.Load() != 0 {
		t.Errorf("scenes=%d lookups=%d, want 0/0", len(scenes), f.starCalls.Load())
	}
}

func TestModelPage(t *testing.T) {
	f := newFakeSite(t)
	s := f.scraper()
	studioURL := "https://powershotz.com/Aubry%20&%20Tatum"
	scenes, errs, _ := collect(t, s, studioURL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	m := byID(scenes)
	if len(m) != 2 || m["12166033"].ID == "" || m["17195888"].ID == "" {
		t.Fatalf("scenes = %v", scenes)
	}
	if m["12166033"].StudioURL != studioURL {
		t.Errorf("StudioURL = %q", m["12166033"].StudioURL)
	}
	if got := m["17195888"].Performers; !slices.Equal(got, []string{"Ally", "Aubry", "Tatum"}) {
		t.Errorf("performers = %v", got)
	}
}

func TestUnknownModelAndClipURL(t *testing.T) {
	f := newFakeSite(t)
	s := f.scraper()
	for _, u := range []string{"https://powershotz.com/Nobody", "https://powershotz.com/2646655", "https://powershotz.com/pzv046"} {
		scenes, errs, _ := collect(t, s, u, scraper.ListOpts{})
		if len(scenes) != 0 || len(errs) != 1 {
			t.Errorf("%s: scenes=%d errs=%v", u, len(scenes), errs)
		}
	}
}

func TestNormalizePerformers(t *testing.T) {
	got := normalizePerformers([]string{"Ally2", "Ally", "Sindee & Suzi", "Nikki T", "Alex Del Monaco"})
	want := []string{"Ally", "Sindee", "Suzi", "Nikki T", "Alex Del Monaco"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
