package missfetilicious

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/mymemberutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://fetiliciousfans.com", true},
		{"https://www.fetiliciousfans.com/914-rek-je-kontje-uit-voor-mij", true},
		{"https://fetiliciousfans.com/videos", true},
		{"https://rachel-steele.com", false},
		{"https://example.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func makeTestVideos() []mymemberutil.APIVideo {
	return []mymemberutil.APIVideo{
		{
			ID:               100,
			Title:            "Test Scene",
			IsPublished:      true,
			PublishDate:      "2026-04-01T12:00:00.000000Z",
			Duration:         1624,
			ContentMappingID: 101,
			ViewsCount:       200,
			LikesCount:       15,
			StreamPrice:      "25",
			PosterSrc:        "https://cdn.example.com/thumb1.jpg",
		},
		{
			ID:               200,
			Title:            "Another Scene",
			IsPublished:      true,
			PublishDate:      "2026-03-15T10:00:00.000000Z",
			Duration:         900,
			ContentMappingID: 201,
			ViewsCount:       100,
			StreamPrice:      25.0,
			PosterSrc:        "https://cdn.example.com/thumb2.jpg",
			Has4K:            true,
		},
	}
}

const detailHTML = `<!DOCTYPE html><html><head>
<meta property="og:description" content="Test scene description."/>
<meta property="og:image" content="https://cdn.example.com/og-thumb.jpg"/>
</head><body>
<script>self.__next_f.push([1,"keywords\":\"Miss Fetilicious, Corset, Dirty talk, Dominant\""])</script>
</body></html>`

func newTestServer(videos []mymemberutil.APIVideo) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cancellable-request":
			page := mymemberutil.VideosPage{
				CurrentPage: 1,
				LastPage:    1,
				Total:       len(videos),
				PerPage:     30,
				Data:        videos,
			}
			type apiResp struct {
				OK   bool            `json:"ok"`
				Data json.RawMessage `json:"data"`
			}
			outer := apiResp{OK: true}
			outer.Data, _ = json.Marshal(page)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(outer)
		default:
			_, _ = w.Write([]byte(detailHTML))
		}
	}))
}

func TestBuildScene(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(detailHTML))
	}))
	defer ts.Close()

	s := New()
	s.mm.Client = ts.Client()
	s.mm.SiteBase = ts.URL

	vid := makeTestVideos()[0]
	scene, err := s.mm.BuildScene(context.Background(), ts.URL, vid)
	if err != nil {
		t.Fatal(err)
	}

	if scene.ID != "100" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "fetiliciousfans" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Studio != "Miss Fetilicious" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if scene.Duration != 1624 {
		t.Errorf("Duration = %d", scene.Duration)
	}
	if scene.Description != "Test scene description." {
		t.Errorf("Description = %q", scene.Description)
	}
	if len(scene.Performers) != 1 {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if len(scene.Tags) != 3 {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if len(scene.PriceHistory) != 1 || scene.PriceHistory[0].Regular != 25 {
		t.Errorf("PriceHistory = %v", scene.PriceHistory)
	}
}

func TestListScenes(t *testing.T) {
	videos := makeTestVideos()
	ts := newTestServer(videos)
	defer ts.Close()

	s := New()
	s.mm.Client = ts.Client()
	s.mm.SiteBase = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	videos := makeTestVideos()
	ts := newTestServer(videos)
	defer ts.Close()

	s := New()
	s.mm.Client = ts.Client()
	s.mm.SiteBase = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"200": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(scenes) != 1 || scenes[0].ID != "100" {
		t.Errorf("got %d scenes, want [100]", len(scenes))
	}
}
