package teencoreclub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func siteConfigJSON(siteID int, studios []configStudio) string {
	cfg := siteConfig{ID: siteID, Studios: studios}
	b, _ := json.Marshal(cfg)
	return string(b)
}

func browsePageJSON(total, lastPage, currentPage int, items []videoItem) string {
	resp := browseResponse{}
	resp.Videos.Total = total
	resp.Videos.LastPage = lastPage
	resp.Videos.CurrentPage = currentPage
	resp.Videos.Data = items
	resp.SceneCount = total
	b, _ := json.Marshal(resp)
	return string(b)
}

func detailJSON(d videoDetail) string {
	b, _ := json.Marshal(d)
	return string(b)
}

func ls(s string) langString { return langString{"en": s} }

func art(url string) artworkObj { return artworkObj{Large: url} }

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://teencoreclub.com", true},
		{"https://teencoreclub.com/", true},
		{"https://www.teencoreclub.com/video/some-slug", true},
		{"https://teencoreclub.com/video/some-slug", true},
		{"https://fabsluts.com", false},
		{"https://tryteens.com", false},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Try Teens", "try-teens"},
		{"BIC", "bic"},
		{"Young Throats", "young-throats"},
		{"Girls Got Cream", "girls-got-cream"},
		{"AdultLabs (461)", "adultlabs-461"},
		{"HardcoreYouth", "hardcoreyouth"},
	}
	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToScene(t *testing.T) {
	item := videoItem{
		ID:              100,
		Title:           ls("Great Scene Title"),
		Slug:            "great-scene-title",
		Artwork:         art("https://cdn.example.com/100.jpg"),
		PublicationDate: "2024-06-15",
		Actors:          []actorItem{{ID: 1, Name: "Jane Doe"}, {ID: 2, Name: "John Smith"}},
		Meta:            videoMeta{DurationSeconds: 1800, Year: 2024},
		Views:           5000,
	}

	detail := &videoDetail{
		Description: "A detailed description",
		Genres:      []genreItem{{ID: 1, Name: "Hardcore"}, {ID: 2, Name: "Blowjob"}},
		Labels:      []labelItem{{ID: 196, Name: "HardcoreYouth.com"}},
		Studio:      &detailStudio{ID: 1624, Name: "BIC", Slug: "bic"},
	}

	studio := configStudio{ID: 1624, Name: "BIC"}
	now := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	scene := toScene(item, detail, studio, "https://teencoreclub.com", now)

	if scene.ID != "100" {
		t.Errorf("ID = %q, want %q", scene.ID, "100")
	}
	if scene.SiteID != "hardcoreyouth" {
		t.Errorf("SiteID = %q, want %q", scene.SiteID, "hardcoreyouth")
	}
	if scene.Title != "Great Scene Title" {
		t.Errorf("Title = %q, want %q", scene.Title, "Great Scene Title")
	}
	if scene.URL != "https://teencoreclub.com/video/great-scene-title" {
		t.Errorf("URL = %q, want %q", scene.URL, "https://teencoreclub.com/video/great-scene-title")
	}
	if scene.Thumbnail != "https://cdn.example.com/100.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if scene.Duration != 1800 {
		t.Errorf("Duration = %d, want 1800", scene.Duration)
	}
	if scene.Description != "A detailed description" {
		t.Errorf("Description = %q", scene.Description)
	}
	if len(scene.Performers) != 2 || scene.Performers[0] != "Jane Doe" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if len(scene.Tags) != 2 || scene.Tags[0] != "Hardcore" {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Views != 5000 {
		t.Errorf("Views = %d, want 5000", scene.Views)
	}
	if scene.Studio != "BIC" {
		t.Errorf("Studio = %q, want %q", scene.Studio, "BIC")
	}
	if scene.Date.Format("2006-01-02") != "2024-06-15" {
		t.Errorf("Date = %v", scene.Date)
	}
}

func TestToSceneNoDetail(t *testing.T) {
	item := videoItem{
		ID:              200,
		Title:           ls("BIC_301"),
		Slug:            "bic-301",
		Artwork:         art("https://cdn.example.com/200.jpg"),
		PublicationDate: "2023-01-10",
		Actors:          []actorItem{{ID: 5, Name: "Alice"}},
		Meta:            videoMeta{DurationSeconds: 900},
		Views:           1000,
	}

	studio := configStudio{ID: 1624, Name: "BIC"}
	now := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	scene := toScene(item, nil, studio, "https://teencoreclub.com", now)

	if scene.ID != "200" {
		t.Errorf("ID = %q, want %q", scene.ID, "200")
	}
	if scene.SiteID != "bic" {
		t.Errorf("SiteID = %q, want %q", scene.SiteID, "bic")
	}
	if scene.Title != "BIC_301" {
		t.Errorf("Title = %q, want %q", scene.Title, "BIC_301")
	}
	if scene.Description != "" {
		t.Errorf("Description = %q, want empty", scene.Description)
	}
	if len(scene.Tags) != 0 {
		t.Errorf("Tags = %v, want empty", scene.Tags)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Alice" {
		t.Errorf("Performers = %v", scene.Performers)
	}
}

func TestToSceneLabelStripsComDomain(t *testing.T) {
	item := videoItem{ID: 300, Title: ls("Test"), Slug: "test"}
	detail := &videoDetail{
		Labels: []labelItem{{ID: 220, Name: "YoungThroats.com"}},
	}
	studio := configStudio{ID: 1624, Name: "BIC"}
	now := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	scene := toScene(item, detail, studio, "https://teencoreclub.com", now)

	if scene.SiteID != "youngthroats" {
		t.Errorf("SiteID = %q, want %q", scene.SiteID, "youngthroats")
	}
}

func TestToSceneDateFormats(t *testing.T) {
	tests := []struct {
		pubDate string
		want    string
	}{
		{"2024-06-15", "2024-06-15"},
		{"2024-06-15T00:00:00.000000Z", "2024-06-15"},
		{"", "0001-01-01"},
	}

	now := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range tests {
		item := videoItem{ID: 1, Slug: "x", PublicationDate: tt.pubDate}
		scene := toScene(item, nil, configStudio{Name: "test"}, "https://teencoreclub.com", now)
		got := scene.Date.Format("2006-01-02")
		if got != tt.want {
			t.Errorf("pubDate=%q → date %q, want %q", tt.pubDate, got, tt.want)
		}
	}
}

func TestFullScrape(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sitecfg":
			_, _ = fmt.Fprint(w, siteConfigJSON(10, []configStudio{
				{ID: 1624, Name: "BIC"},
			}))
		case "/api/videos/browse/studio/1624":
			_, _ = fmt.Fprint(w, browsePageJSON(2, 1, 1, []videoItem{
				{
					ID: 100, Title: ls("Scene 1"), Slug: "scene-1",
					Artwork: art("https://cdn.example.com/100.jpg"), PublicationDate: "2024-06-15",
					Actors: []actorItem{{ID: 1, Name: "Jane Doe"}},
					Meta:   videoMeta{DurationSeconds: 1800}, Views: 5000,
				},
				{
					ID: 101, Title: ls("Scene 2"), Slug: "scene-2",
					Artwork: art("https://cdn.example.com/101.jpg"), PublicationDate: "2024-06-14",
					Actors: []actorItem{{ID: 2, Name: "Jane Smith"}},
					Meta:   videoMeta{DurationSeconds: 2400}, Views: 3000,
				},
			}))
		case "/api/videodetail/100":
			_, _ = fmt.Fprint(w, detailJSON(videoDetail{
				ID: 100, Title: ls("Scene 1"), Slug: "scene-1",
				Description: "A great scene",
				Genres:      []genreItem{{ID: 1, Name: "Hardcore"}},
				Labels:      []labelItem{{ID: 196, Name: "HardcoreYouth.com"}},
				Studio:      &detailStudio{ID: 1624, Name: "BIC"},
			}))
		case "/api/videodetail/101":
			_, _ = fmt.Fprint(w, detailJSON(videoDetail{
				ID: 101, Title: ls("Scene 2"), Slug: "scene-2",
				Description: "Another scene",
				Genres:      []genreItem{{ID: 2, Name: "Blowjob"}},
				Labels:      []labelItem{{ID: 220, Name: "YoungThroats.com"}},
				Studio:      &detailStudio{ID: 1624, Name: "BIC"},
			}))
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	old := apiBase
	apiBase = ts.URL
	defer func() { apiBase = old }()

	s := New()
	out, err := s.ListScenes(context.Background(), "https://teencoreclub.com", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes []string
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene.ID)
			if r.Scene.Description == "" {
				t.Errorf("scene %s missing description", r.Scene.ID)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}

	if len(scenes) != 2 {
		t.Errorf("got %d scenes, want 2", len(scenes))
	}
}

func TestEarlyStop(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sitecfg":
			_, _ = fmt.Fprint(w, siteConfigJSON(10, []configStudio{
				{ID: 1, Name: "TestStudio"},
			}))
		case "/api/videos/browse/studio/1":
			_, _ = fmt.Fprint(w, browsePageJSON(1, 1, 1, []videoItem{
				{ID: 50, Title: ls("Known"), Slug: "known", PublicationDate: "2024-01-01"},
			}))
		case "/api/videodetail/50":
			_, _ = fmt.Fprint(w, detailJSON(videoDetail{ID: 50, Title: ls("Known"), Slug: "known"}))
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	old := apiBase
	apiBase = ts.URL
	defer func() { apiBase = old }()

	s := New()
	out, err := s.ListScenes(context.Background(), "https://teencoreclub.com", scraper.ListOpts{
		KnownIDs: map[string]bool{"50": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotEarlyStop bool
	for r := range out {
		if r.Kind == scraper.KindStoppedEarly {
			gotEarlyStop = true
		}
		if r.Kind == scraper.KindScene {
			t.Error("should not have received a scene")
		}
	}
	if !gotEarlyStop {
		t.Error("expected early stop signal")
	}
}

func TestPagination(t *testing.T) {
	page2Called := false

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/sitecfg":
			_, _ = fmt.Fprint(w, siteConfigJSON(10, []configStudio{
				{ID: 1, Name: "TestStudio"},
			}))
		case r.URL.Path == "/api/videos/browse/studio/1":
			page := r.URL.Query().Get("page")
			if page == "2" {
				page2Called = true
				_, _ = fmt.Fprint(w, browsePageJSON(60, 2, 2, []videoItem{
					{ID: 51, Title: ls("Page 2 Scene"), Slug: "page-2", PublicationDate: "2024-01-01"},
				}))
				return
			}
			items := make([]videoItem, 50)
			for i := range items {
				items[i] = videoItem{
					ID:              i + 1,
					Title:           ls(fmt.Sprintf("Scene %d", i+1)),
					Slug:            fmt.Sprintf("scene-%d", i+1),
					PublicationDate: "2024-01-01",
				}
			}
			_, _ = fmt.Fprint(w, browsePageJSON(60, 2, 1, items))
		case len(r.URL.Path) > len("/api/videodetail/") && r.URL.Path[:len("/api/videodetail/")] == "/api/videodetail/":
			_, _ = fmt.Fprint(w, detailJSON(videoDetail{ID: 1, Title: ls("Detail"), Slug: "detail"}))
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	old := apiBase
	apiBase = ts.URL
	defer func() { apiBase = old }()

	s := New()
	out, err := s.ListScenes(context.Background(), "https://teencoreclub.com", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	count := 0
	for r := range out {
		if r.Kind == scraper.KindScene {
			count++
		}
	}

	if !page2Called {
		t.Error("page 2 was never fetched")
	}
	if count != 51 {
		t.Errorf("got %d scenes, want 51", count)
	}
}

func TestLangString(t *testing.T) {
	tests := []struct {
		input langString
		want  string
	}{
		{langString{"en": "Hello"}, "Hello"},
		{langString{"de": "Hallo", "en": "Hello"}, "Hello"},
		{langString{"de": "Hallo"}, "Hallo"},
		{langString{}, ""},
		{nil, ""},
	}
	for _, tt := range tests {
		got := tt.input.String()
		if got != tt.want {
			t.Errorf("langString(%v).String() = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- golden fixtures ---------------------------------------------------------
//
// The other tests build videoItem/siteConfig values in Go, so encode and decode
// share the struct tag and a renamed one round-trips unnoticed. These are
// byte-verbatim slices of live api.fundorado.com responses.
//
// Reaching the API needs `Referer`/`Origin` headers for teencoreclub.com; it was
// previously recorded as returning "its SPA shell without real studio/site IDs",
// but /api/sitecfg?h=teencoreclub.com answers with the real config. No credential
// is involved.
//
// Both bodies are trimmed to the fields the structs decode plus a few array
// entries — sitecfg alone is 104KB, of which the scraper reads only `id` and
// `studios` — following the same precedent as gammautil dropping
// `_highlightResult`. Each retained object is unedited.
//
// Shapes a hand-written fixture would have got wrong:
//   - **`title` is a language object, `{"en": "…"}`, not a string.** langString
//     exists for this, and guessing a plain string loses every title.
//   - `artwork` is an object of four named sizes, not a URL.
//   - `publication_date` carries **six-digit microseconds** and a Z
//     ("2020-06-24T22:00:00.000000Z"), which is why toScene tries a bare
//     "2006-01-02" first and then an explicit microsecond layout.
//   - `videos.total` and `videos.last_page` are nested under `videos`, not at the
//     top level, and the walk terminates on them.
func TestGoldenSiteConfig(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "sitecfg.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg siteConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if cfg.ID != 10 {
		t.Errorf("ID = %d (id), want 10 — it becomes site_id on every browse call", cfg.ID)
	}
	if len(cfg.Studios) != 3 {
		t.Fatalf("decoded %d studios, want 3", len(cfg.Studios))
	}
	if cfg.Studios[0].ID == 0 || cfg.Studios[0].Name == "" {
		t.Errorf("studio[0] = %+v (studios[].id/name)", cfg.Studios[0])
	}
}

func TestGoldenBrowse(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "browse.json"))
	if err != nil {
		t.Fatal(err)
	}
	var resp browseResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if len(resp.Videos.Data) != 2 {
		t.Fatalf("decoded %d videos, want 2", len(resp.Videos.Data))
	}
	if resp.Videos.Total != 1768 {
		t.Errorf("Total = %d (videos.total), want 1768", resp.Videos.Total)
	}
	if resp.Videos.LastPage != 36 {
		t.Errorf("LastPage = %d (videos.last_page), want 36 — the walk stops on it", resp.Videos.LastPage)
	}
	if resp.Videos.CurrentPage != 1 {
		t.Errorf("CurrentPage = %d (videos.current_page)", resp.Videos.CurrentPage)
	}

	it := resp.Videos.Data[0]
	if it.ID == 0 {
		t.Error("ID = 0 (id)")
	}
	if it.Slug == "" {
		t.Error("Slug is empty (slug)")
	}
	// The language-object title.
	if got := it.Title.String(); got != "BIC_1360692388" {
		t.Errorf("Title.String() = %q, want the en value — title is a {\"en\": …} object, not a string", got)
	}
	if _, ok := it.Title["en"]; !ok {
		t.Errorf("title has no \"en\" key: %v", it.Title)
	}
	if it.Artwork.Original == "" || it.Artwork.Large == "" {
		t.Errorf("artwork = %+v (artwork.original/large) — it is an object of sizes, not a URL", it.Artwork)
	}

	// The microsecond timestamp, and that the layouts toScene uses still match it.
	if it.PublicationDate != "2020-06-24T22:00:00.000000Z" {
		t.Errorf("PublicationDate = %q (publication_date), want six-digit microseconds", it.PublicationDate)
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000000Z", it.PublicationDate); err != nil {
		t.Errorf("publication_date %q does not parse with the layout toScene uses: %v", it.PublicationDate, err)
	}
}

// The captured bodies must stay captures.
func TestGoldenFixturesAreRawCaptures(t *testing.T) {
	browse, err := os.ReadFile(filepath.Join("testdata", "browse.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(browse, []byte(`"title":{"en":`)) {
		t.Error(`browse fixture lost the compact "title":{"en": form — it looks re-encoded`)
	}
	if !bytes.Contains(browse, []byte(`"publication_date":"2020-06-24T22:00:00.000000Z"`)) {
		t.Error("browse fixture lost the microsecond timestamp verbatim")
	}
}

// SiteID is derived from the detail's label, so a scene whose detail failed
// must not be emitted from the listing alone — it would be filed under a
// different identity than the same scene gets on a clean run, and `(ID, SiteID)`
// is the store's key. The failure is reported so the run counts as incomplete.
func TestDetailFailureReportsAndSkipsRatherThanReFilingTheScene(t *testing.T) {
	var detailHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/api/videodetail/"):
			atomic.AddInt32(&detailHits, 1)
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("unexpected fetch %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	restore := apiBase
	apiBase = srv.URL
	defer func() { apiBase = restore }()

	s := New()
	s.client = srv.Client()

	out := make(chan scraper.SceneResult, 16)
	items := []videoItem{{ID: 1}, {ID: 2}}
	scenes, err := s.enrichAndCollect(context.Background(), items,
		configStudio{Name: "Teen Core Club"}, "https://teencoreclub.com/", scraper.ListOpts{Workers: 1}, out)
	close(out)
	if err != nil {
		t.Fatalf("enrichAndCollect: %v", err)
	}
	if len(scenes) != 0 {
		t.Errorf("got %d scenes, want 0 — a scene with no label must not be filed", len(scenes))
	}
	errs := 0
	for r := range out {
		if r.Kind == scraper.KindError {
			errs++
		}
	}
	if errs != len(items) {
		t.Errorf("reported %d errors, want %d", errs, len(items))
	}
}
