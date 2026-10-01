package gammautil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestParseDate(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"2026-01-15", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)},
		{"", time.Time{}},
		{"not-a-date", time.Time{}},
		{"2026-01-15T12:00:00Z", time.Time{}}, // wrong format for this site
	}
	for _, c := range cases {
		got := ParseDate(c.in)
		if !got.Equal(c.want) {
			t.Errorf("ParseDate(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseFormatHeight(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"1080p", 1080},
		{"720p", 720},
		{"720", 720},
		{"", 0},
		{"abc", 0},
	}
	for _, c := range cases {
		if got := parseFormatHeight(c.in); got != c.want {
			t.Errorf("parseFormatHeight(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestBestResolution(t *testing.T) {
	t.Run("4k master category overrides formats", func(t *testing.T) {
		w, h, r := BestResolution(
			[]VideoFormat{{Format: "720p"}},
			[]string{"4k"},
		)
		if w != 3840 || h != 2160 || r != "2160p" {
			t.Errorf("got %d/%d/%q, want 3840/2160/2160p", w, h, r)
		}
	})

	t.Run("picks highest format height", func(t *testing.T) {
		w, h, r := BestResolution(
			[]VideoFormat{{Format: "480p"}, {Format: "1080p"}, {Format: "720p"}},
			nil,
		)
		if w != 1920 || h != 1080 || r != "1080p" {
			t.Errorf("got %d/%d/%q", w, h, r)
		}
	})

	t.Run("empty formats returns zeros", func(t *testing.T) {
		w, h, r := BestResolution(nil, nil)
		if w != 0 || h != 0 || r != "" {
			t.Errorf("got %d/%d/%q, want all zero", w, h, r)
		}
	})

	t.Run("unknown height yields zero width", func(t *testing.T) {
		w, h, r := BestResolution([]VideoFormat{{Format: "999p"}}, nil)
		if w != 0 || h != 999 || r != "999p" {
			t.Errorf("got %d/%d/%q", w, h, r)
		}
	})
}

func TestThumbnailURL(t *testing.T) {
	t.Run("prefers Full1920", func(t *testing.T) {
		got := ThumbnailURL(Pictures{Full1920: "/path/a.jpg", Res638: "/path/b.jpg"})
		want := ImageCDN + "/path/a.jpg"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("falls back to NSFW.Top.Full1920", func(t *testing.T) {
		var pics Pictures
		pics.NSFW.Top.Full1920 = "/nsfw/top.jpg"
		got := ThumbnailURL(pics)
		want := ImageCDN + "/nsfw/top.jpg"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("falls back to Res638", func(t *testing.T) {
		got := ThumbnailURL(Pictures{Res638: "/small.jpg"})
		want := ImageCDN + "/small.jpg"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("empty returns empty", func(t *testing.T) {
		if got := ThumbnailURL(Pictures{}); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestBestTrailer(t *testing.T) {
	t.Run("picks highest-resolution trailer URL", func(t *testing.T) {
		formats := []VideoFormat{
			{Format: "480p", TrailerURL: "https://cdn.example/480.mp4"},
			{Format: "1080p", TrailerURL: "https://cdn.example/1080.mp4"},
			{Format: "720p", TrailerURL: "https://cdn.example/720.mp4"},
		}
		if got := BestTrailer(formats); got != "https://cdn.example/1080.mp4" {
			t.Errorf("got %q, want 1080 URL", got)
		}
	})

	t.Run("skips formats with empty trailer URL", func(t *testing.T) {
		formats := []VideoFormat{
			{Format: "1080p", TrailerURL: ""},
			{Format: "720p", TrailerURL: "https://cdn.example/720.mp4"},
		}
		if got := BestTrailer(formats); got != "https://cdn.example/720.mp4" {
			t.Errorf("got %q, want 720 URL", got)
		}
	})

	t.Run("empty formats returns empty", func(t *testing.T) {
		if got := BestTrailer(nil); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestToScene(t *testing.T) {
	cfg := SiteConfig{
		SiteID:     "puretaboo",
		SiteBase:   "https://www.puretaboo.com",
		StudioName: "Pure Taboo",
		SiteName:   "puretaboo",
	}
	hit := AlgoliaHit{
		ClipID:      9999,
		Title:       "Family Therapy",
		Description: "First line.<br>Second line.&amp; more",
		ReleaseDate: "2026-01-15",
		URLTitle:    "family-therapy",
		SerieName:   "Therapy Series",
		Actors: []Actor{
			{ActorID: "1", Name: "Alice"},
			{ActorID: "2", Name: "Bob"},
		},
		Directors: []Director{
			{Name: "Director One"},
			{Name: "Director Two"},
		},
		Categories: []Category{
			{Name: "Drama"},
			{Name: "Roleplay"},
		},
		VideoFormats: []VideoFormat{
			{Format: "1080p", TrailerURL: "https://cdn.example/1080.mp4"},
		},
		Pictures:         Pictures{Full1920: "/cdn/cover.jpg"},
		MasterCategories: nil,
		Length:           1800,
		RatingsUp:        42,
	}
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)

	got := ToScene(cfg, "https://www.puretaboo.com", hit, now)

	if got.ID != "9999" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.SiteID != "puretaboo" {
		t.Errorf("SiteID = %q", got.SiteID)
	}
	if got.Title != "Family Therapy" {
		t.Errorf("Title = %q", got.Title)
	}
	wantURL := "https://www.puretaboo.com/en/video/puretaboo/family-therapy/9999"
	if got.URL != wantURL {
		t.Errorf("URL = %q, want %q", got.URL, wantURL)
	}
	if got.Studio != "Pure Taboo" {
		t.Errorf("Studio = %q", got.Studio)
	}
	wantDesc := "First line.\nSecond line.& more"
	if got.Description != wantDesc {
		t.Errorf("Description = %q, want %q", got.Description, wantDesc)
	}
	if got.Thumbnail != ImageCDN+"/cdn/cover.jpg" {
		t.Errorf("Thumbnail = %q", got.Thumbnail)
	}
	if got.Preview != "https://cdn.example/1080.mp4" {
		t.Errorf("Preview = %q", got.Preview)
	}
	if len(got.Performers) != 2 || got.Performers[0] != "Alice" || got.Performers[1] != "Bob" {
		t.Errorf("Performers = %v", got.Performers)
	}
	if got.Director != "Director One, Director Two" {
		t.Errorf("Director = %q", got.Director)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "Drama" || got.Tags[1] != "Roleplay" {
		t.Errorf("Tags = %v", got.Tags)
	}
	if got.Series != "Therapy Series" {
		t.Errorf("Series = %q", got.Series)
	}
	if got.Width != 1920 || got.Height != 1080 || got.Resolution != "1080p" {
		t.Errorf("resolution = %d/%d/%q", got.Width, got.Height, got.Resolution)
	}
	if got.Duration != 1800 {
		t.Errorf("Duration = %d", got.Duration)
	}
	if got.Likes != 42 {
		t.Errorf("Likes = %d", got.Likes)
	}
	if got.Date.IsZero() {
		t.Error("Date is zero")
	}
	if !got.ScrapedAt.Equal(now) {
		t.Errorf("ScrapedAt = %v", got.ScrapedAt)
	}
}

func TestFetchAPIKey(t *testing.T) {
	t.Run("extracts key from page source", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/en/videos" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`<html><script>window.__data={"algolia":{"appId":"X","apiKey":"abc123def456"}};</script></html>`))
		}))
		defer ts.Close()

		s := &Scraper{Client: ts.Client(), cfg: SiteConfig{SiteBase: ts.URL}}
		key, err := s.FetchAPIKey(context.Background())
		if err != nil {
			t.Fatalf("FetchAPIKey error: %v", err)
		}
		if key != "abc123def456" {
			t.Errorf("key = %q", key)
		}
	})

	t.Run("returns error when key not in page", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<html>no algolia config here</html>`))
		}))
		defer ts.Close()

		s := &Scraper{Client: ts.Client(), cfg: SiteConfig{SiteBase: ts.URL}}
		if _, err := s.FetchAPIKey(context.Background()); err == nil {
			t.Error("expected error when API key missing")
		}
	})
}

func TestFetchPage(t *testing.T) {
	hits := []AlgoliaHit{
		{ClipID: 1, Title: "Scene One", ReleaseDate: "2026-01-15"},
		{ClipID: 2, Title: "Scene Two", ReleaseDate: "2026-01-14"},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AlgoliaResponse{Hits: hits, NbHits: 2})
	}))
	defer ts.Close()

	s := &Scraper{
		Client:      ts.Client(),
		cfg:         SiteConfig{SiteName: "puretaboo", SiteBase: "https://example.com"},
		AlgoliaHost: ts.URL,
	}
	got, total, err := s.FetchPage(context.Background(), "test-key", 0)
	if err != nil {
		t.Fatalf("FetchPage error: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(got) != 2 || got[0].ClipID != 1 || got[1].ClipID != 2 {
		t.Errorf("hits = %+v", got)
	}
}

func TestFetchPageActorFilter(t *testing.T) {
	var capturedFilter string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q AlgoliaQuery
		if err := json.NewDecoder(r.Body).Decode(&q); err == nil {
			capturedFilter = q.Filters
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AlgoliaResponse{
			Hits:   []AlgoliaHit{{ClipID: 42, Title: "Filtered Scene"}},
			NbHits: 1,
		})
	}))
	defer ts.Close()

	s := &Scraper{
		Client:      ts.Client(),
		cfg:         SiteConfig{SiteName: "lethalhardcore", SiteBase: "https://example.com"},
		AlgoliaHost: ts.URL,
	}
	_, _, err := s.FetchPage(context.Background(), "test-key", 0, "actors.actor_id:111676")
	if err != nil {
		t.Fatalf("FetchPage error: %v", err)
	}
	want := "availableOnSite:lethalhardcore AND upcoming:0 AND actors.actor_id:111676"
	if capturedFilter != want {
		t.Errorf("filter = %q, want %q", capturedFilter, want)
	}
}

func TestActorURLParsing(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.lethalhardcore.com/en/pornstar/view/Syren-De-Mer/111676", "111676"},
		{"https://www.evilangel.com/en/pornstar/view/Angela-White/48859", "48859"},
		{"https://www.playboytv.com/en/model/view/Alana-Rey/123147", "123147"},
		{"https://www.lethalhardcore.com/en/videos", ""},
		{"https://www.lethalhardcore.com", ""},
	}
	for _, c := range cases {
		m := actorURLRe.FindStringSubmatch(c.url)
		got := ""
		if m != nil {
			got = m[1]
		}
		if got != c.want {
			t.Errorf("actorURLRe(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build AlgoliaHit values in Go, so encode and decode share the
// struct tag and a renamed tag round-trips unnoticed. This one serves a response
// captured verbatim from the live Algolia index (the search key is scraped from
// the site's own page source at runtime, so no credential is involved).
//
// Only Algolia's generated `_highlightResult` blob was dropped — the scraper never
// reads it and it tripled the fixture size. Every field AlgoliaHit decodes is
// verbatim.
//
// Notable shapes: `clip_length` is "00:58:20" alongside a numeric `length`,
// `actor_id`/`category_id` are JSON *strings* inside otherwise numeric records,
// and `pictures` is a deeply nested map keyed by resolution.
func TestGoldenAlgoliaQuery(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "algolia_query.json"))
	if err != nil {
		t.Fatal(err)
	}
	var resp AlgoliaResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if len(resp.Hits) != 2 {
		t.Fatalf("decoded %d hits, want 2", len(resp.Hits))
	}
	if resp.NbHits == 0 {
		t.Error("NbHits is 0 (nbHits) — pagination depends on it")
	}

	h := resp.Hits[0]
	if h.ClipID != 287715 {
		t.Errorf("ClipID = %d (clip_id), want 287715", h.ClipID)
	}
	if h.Title == "" {
		t.Error("Title is empty (title)")
	}
	if h.ClipLength != "00:58:20" {
		t.Errorf("ClipLength = %q (clip_length)", h.ClipLength)
	}
	if h.Length != 3500 {
		t.Errorf("Length = %d (length), want 3500", h.Length)
	}
	if h.ReleaseDate != "2026-07-03" {
		t.Errorf("ReleaseDate = %q (release_date)", h.ReleaseDate)
	}
	if h.SiteName != "wicked" || h.SiteNamePretty != "Wicked" {
		t.Errorf("sitename/sitename_pretty = %q/%q", h.SiteName, h.SiteNamePretty)
	}
	if h.URLTitle == "" {
		t.Error("URLTitle is empty (url_title) — the scene URL is built from it")
	}
	if h.ObjectID == "" {
		t.Error("ObjectID is empty (objectID)")
	}
	// actor_id is a string inside an otherwise numeric-looking record.
	if len(h.Actors) == 0 || h.Actors[0].Name == "" || h.Actors[0].ActorID == "" {
		t.Errorf("Actors = %+v (actors[].name/.actor_id)", h.Actors)
	}
	if len(h.Directors) == 0 || h.Directors[0].Name == "" {
		t.Errorf("Directors = %+v (directors[].name)", h.Directors)
	}
	if len(h.Categories) == 0 || h.Categories[0].Name == "" {
		t.Errorf("Categories = %+v (categories[].name)", h.Categories)
	}
	if h.RatingsUp == 0 {
		t.Error("RatingsUp is 0 (ratings_up)")
	}
}

// A URL naming a channel, studio or sub-site used to produce no filter at all,
// so the scrape returned the segment's whole catalogue and stored it under that
// URL's key. Which facet answers depends on the segment — ASGMAX indexes no
// channels.id, Adult Time indexes both — so the URL form picks the first to try
// and the other is the fallback.
func TestFilterForURL(t *testing.T) {
	// The stub answers a filter only if it is one the "index" knows.
	indexed := map[string]int{
		"upcoming:0 AND channels.id:girlsway":           2502,
		"upcoming:0 AND availableOnSite:strokethatdick": 78,
	}
	var asked []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q AlgoliaQuery
		_ = json.NewDecoder(r.Body).Decode(&q)
		asked = append(asked, q.Filters)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AlgoliaResponse{NbHits: indexed[q.Filters]})
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), cfg: SiteConfig{SiteBase: "https://example.com"}, AlgoliaHost: ts.URL}

	for _, tc := range []struct{ name, url, want string }{
		{"channel", "https://example.com/en/channel/girlsway", "channels.id:girlsway"},
		{"studio alias", "https://example.com/en/studio/girlsway", "channels.id:girlsway"},
		{"sub-site listing", "https://example.com/en/videos/sites/strokethatdick", "availableOnSite:strokethatdick"},
		{"performer", "https://example.com/en/pornstar/view/someone/4242", "actors.actor_id:4242"},
		{"series", "https://example.com/en/serie/99/", "serie_id:99"},
		{"catalogue", "https://example.com/en/videos", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.FilterForURL(context.Background(), "k", tc.url)
			if err != nil {
				t.Fatalf("FilterForURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("filter = %q, want %q", got, tc.want)
			}
		})
	}

	// The sub-site form is asked availableOnSite first; the channel form is
	// asked channels.id first. Getting that backwards costs a request per run
	// and, on a slug both facets know, the wrong scope.
	t.Run("tries the URL form's own facet first", func(t *testing.T) {
		asked = nil
		if _, err := s.FilterForURL(context.Background(), "k", "https://example.com/en/channel/girlsway"); err != nil {
			t.Fatal(err)
		}
		if len(asked) != 1 || !strings.Contains(asked[0], "channels.id:girlsway") {
			t.Errorf("asked %v, want one channels.id query", asked)
		}
	})
}

// A slug neither facet knows must be an error. Falling back to no filter would
// scrape the whole segment — 70,051 scenes on Adult Time — under that URL's
// store key, which is the bug this dispatch exists to prevent.
func TestFilterForURLRefusesToWidenAnUnknownSlug(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AlgoliaResponse{NbHits: 0})
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), cfg: SiteConfig{SiteBase: "https://example.com"}, AlgoliaHost: ts.URL}
	got, err := s.FilterForURL(context.Background(), "k", "https://example.com/en/channel/nope")
	if err == nil {
		t.Fatalf("filter = %q, want an error rather than an unfiltered walk", got)
	}
	if kind := scraper.Classify(err); !kind.MissingData() {
		t.Errorf("error classifies as %v, which would let --full delete the catalogue", kind)
	}
}
