package lukespov

import (
	"context"
	"encoding/json"
	"fmt"
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
		{"https://lukespov.vip", true},
		{"https://www.lukespov.vip/005-videos", true},
		{"https://lukespov.vip/4943-milf1928-breakfast-fuck-3", true},
		{"https://www.manyvids.com/Profile/123/foo", false},
		{"https://lukespov.com/", false},
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
			ID:                        100,
			Title:                     "MILF100 - Test Scene One",
			IsPublished:               true,
			PublishDate:               "2026-04-01T12:00:00.000000Z",
			Duration:                  900,
			ContentMappingID:          101,
			ViewsCount:                500,
			LikesCount:                20,
			CommentsCount:             5,
			StreamPrice:               19.99,
			PosterSrc:                 "https://cdn.example.com/thumb1.jpg",
			SystemPreviewVideoFullSrc: "https://cdn.example.com/preview1.mp4",
		},
		{
			ID:                        200,
			Title:                     "MILF200 - Test Scene Two",
			IsPublished:               true,
			PublishDate:               "2026-03-15T10:00:00.000000Z",
			Duration:                  1200,
			ContentMappingID:          201,
			ViewsCount:                300,
			LikesCount:                10,
			StreamPrice:               "24.99",
			PosterSrc:                 "https://cdn.example.com/thumb2.jpg",
			SystemPreviewVideoFullSrc: "https://cdn.example.com/preview2.mp4",
			Has4K:                     true,
		},
	}
}

const detailPageHTML = `<!DOCTYPE html><html><head>
<meta property="og:description" content="Scene description for testing."/>
<meta property="og:image" content="https://cdn.example.com/og-thumb.jpg"/>
</head><body>
<script>self.__next_f.push([1,"keywords\":\"Luke Riggs, Gia Rouge, 4K, POV, Blowjob\""])</script>
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
			_, _ = w.Write([]byte(detailPageHTML))
		}
	}))
}

func TestBuildScene(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(detailPageHTML))
	}))
	defer ts.Close()

	s := New()
	s.mm.Client = ts.Client()
	s.mm.SiteBase = ts.URL

	vid := makeTestVideos()[1]
	scene, err := s.mm.BuildScene(context.Background(), ts.URL, vid)
	if err != nil {
		t.Fatal(err)
	}

	if scene.ID != "200" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "lukespov" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Title != "MILF200 - Test Scene Two" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Duration != 1200 {
		t.Errorf("Duration = %d", scene.Duration)
	}
	if scene.Width != 3840 || scene.Height != 2160 {
		t.Errorf("Resolution: %dx%d", scene.Width, scene.Height)
	}
	if scene.Studio != "Lukes POV" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if scene.Views != 300 {
		t.Errorf("Views = %d", scene.Views)
	}
	if scene.Description != "Scene description for testing." {
		t.Errorf("Description = %q", scene.Description)
	}
	if len(scene.Performers) != 2 {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if len(scene.Tags) != 3 {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Thumbnail != "https://cdn.example.com/og-thumb.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if len(scene.PriceHistory) != 1 || scene.PriceHistory[0].Regular != 24.99 {
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

func TestListScenesSkipsUnpublished(t *testing.T) {
	videos := []mymemberutil.APIVideo{
		{ID: 1, Title: "Published", IsPublished: true, PublishDate: "2026-01-01T00:00:00.000000Z", ContentMappingID: 1},
		{ID: 2, Title: "Unpublished", IsPublished: false, PublishDate: "2026-01-02T00:00:00.000000Z", ContentMappingID: 2},
	}
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
	if len(scenes) != 1 {
		t.Errorf("got %d scenes, want 1 (unpublished should be skipped)", len(scenes))
	}
	if len(scenes) == 1 && scenes[0].Title != "Published" {
		t.Errorf("got %q, expected only Published", scenes[0].Title)
	}
}

func TestMultiPage(t *testing.T) {
	pageData := map[int][]mymemberutil.APIVideo{
		1: {{ID: 1, Title: "Scene A", IsPublished: true, PublishDate: "2026-04-01T00:00:00.000000Z", ContentMappingID: 1}},
		2: {{ID: 2, Title: "Scene B", IsPublished: true, PublishDate: "2026-03-01T00:00:00.000000Z", ContentMappingID: 2}},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cancellable-request":
			args := r.URL.Query().Get("args")
			page := 1
			for p := 1; p <= 3; p++ {
				expected, _ := json.Marshal([]any{[]string{fmt.Sprintf("page=%d", p)}})
				if args == string(expected) {
					page = p
					break
				}
			}
			videos := pageData[page]
			lastPage := 2
			if page > 2 {
				videos = nil
			}
			vp := mymemberutil.VideosPage{
				CurrentPage: page,
				LastPage:    lastPage,
				Total:       2,
				PerPage:     1,
				Data:        videos,
			}
			type apiResp struct {
				OK   bool            `json:"ok"`
				Data json.RawMessage `json:"data"`
			}
			outer := apiResp{OK: true}
			outer.Data, _ = json.Marshal(vp)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(outer)
		default:
			_, _ = w.Write([]byte(detailPageHTML))
		}
	}))
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
