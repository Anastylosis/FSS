package candidlooks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type recorder struct {
	mu    sync.Mutex
	posts []string
}

func (r *recorder) add(q string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.posts = append(r.posts, q)
}

func newServer(t *testing.T, host string, pages map[string]string, totalPages string, rec *recorder) (*Scraper, *httptest.Server) {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/content/wp-json/wp/v2/posts":
			rec.add(r.URL.RawQuery)
			body, ok := pages[r.URL.Query().Get("page")]
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":"rest_post_invalid_page_number"}`))
				return
			}
			w.Header().Set("X-WP-Total", "3")
			w.Header().Set("X-WP-TotalPages", totalPages)
			_, _ = w.Write([]byte(strings.ReplaceAll(body, "https://"+host, ts.URL)))
		case "/content/wp-json/wp/v2/categories":
			if r.URL.Query().Get("slug") == "membervideos" {
				_, _ = w.Write([]byte(`[{"id":6}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case "/content/wp-json/wp/v2/tags":
			_, _ = w.Write([]byte(`[{"id":56}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	cfg := SiteConfig{ID: "amateurlapdancer", Host: host, Name: "Amateur Lapdancer", Base: ts.URL}
	s := New(cfg)
	s.client = ts.Client()
	return s, ts
}

func run(t *testing.T, s *Scraper, url string) ([]scraper.SceneResult, []error, int) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), url, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var scenes []scraper.SceneResult
	var errs []error
	total := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r)
		case scraper.KindError:
			errs = append(errs, r.Err)
		case scraper.KindTotal:
			total = r.Total
		}
	}
	return scenes, errs, total
}

func TestListScenesPaginates(t *testing.T) {
	rec := &recorder{}
	s, ts := newServer(t, "amateurlapdancer.com", map[string]string{
		"1": fixture(t, "al_page1.json"),
		"2": fixture(t, "al_page2.json"),
	}, "2", rec)

	studioURL := "https://amateurlapdancer.com/"
	scenes, errs, total := run(t, s, studioURL)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}

	sc := scenes[0].Scene
	if sc.ID != "867" || sc.SiteID != "amateurlapdancer" || sc.Studio != "Amateur Lapdancer" {
		t.Errorf("ids = %q %q %q", sc.ID, sc.SiteID, sc.Studio)
	}
	if sc.StudioURL != studioURL {
		t.Errorf("StudioURL = %q", sc.StudioURL)
	}
	if sc.Title != "Petite blondie does the best lapdance of her life" {
		t.Errorf("Title = %q", sc.Title)
	}
	if !strings.HasPrefix(sc.URL, ts.URL+"/content/2026/01/") {
		t.Errorf("URL = %q", sc.URL)
	}
	if got := sc.Date.Format("2006-01-02"); got != "2026-01-10" {
		t.Errorf("Date = %s", got)
	}
	if !strings.HasPrefix(sc.Description, "Hey there! Meet Lucy…a fearless girl, who doesn’t want") ||
		!strings.Contains(sc.Description, "More in the video:-)") {
		t.Errorf("Description = %q", sc.Description)
	}
	if strings.Contains(sc.Description, "<") || strings.Contains(sc.Description, "play_box") {
		t.Errorf("Description leaks markup: %q", sc.Description)
	}
	if !strings.HasSuffix(sc.Thumbnail, "/posters/al_video_4625_lucie_stra_1.mp4.jpg") {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if len(sc.Tags) != 9 || sc.Tags[0] != "big boobs" || sc.Tags[8] != "vaginal sex" {
		t.Errorf("Tags = %v (category term must be excluded)", sc.Tags)
	}

	teaser := scenes[1].Scene
	if teaser.ID != "289" || teaser.Description != "" {
		t.Errorf("join_block-only post = %q / %q", teaser.ID, teaser.Description)
	}
	if !strings.HasSuffix(teaser.Thumbnail, "al_video_632_camera_girl.wmv.jpg") {
		t.Errorf("Thumbnail = %q", teaser.Thumbnail)
	}

	inline := scenes[2].Scene
	if inline.Title != "Yana–her very first time in front of a camera" {
		t.Errorf("Title = %q", inline.Title)
	}
	if !strings.HasPrefix(inline.Description, "This is Yana, she has never been naked") ||
		strings.Contains(inline.Description, "Join Now") {
		t.Errorf("Description = %q", inline.Description)
	}

	if len(rec.posts) != 2 {
		t.Errorf("post requests = %v, want 2 pages", rec.posts)
	}
}

func TestPicturePostsSkipped(t *testing.T) {
	rec := &recorder{}
	s, _ := newServer(t, "genuinefeatures.com", map[string]string{"1": fixture(t, "gf_page1.json")}, "1", rec)
	s.cfg.ID, s.cfg.Name = "genuinefeatures", "Genuine Features"

	scenes, errs, _ := run(t, s, "https://genuinefeatures.com/content/")
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1 (picture set dropped)", len(scenes))
	}
	sc := scenes[0].Scene
	if sc.ID != "796" || sc.SiteID != "genuinefeatures" || sc.Studio != "Genuine Features" {
		t.Errorf("scene = %+v", sc)
	}
	if !strings.HasPrefix(sc.Description, "She is real hot.") || strings.Contains(sc.Description, "Join Now") {
		t.Errorf("Description = %q", sc.Description)
	}
	if len(sc.Tags) != 1 || sc.Tags[0] != "Nikola" {
		t.Errorf("Tags = %v", sc.Tags)
	}
}

func TestCategoryAndTagModes(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://amateurlapdancer.com/content/category/membervideos/", "categories=6"},
		{"https://amateurlapdancer.com/content/tag/big-boobs/", "tags=56"},
	}
	for _, c := range cases {
		rec := &recorder{}
		s, _ := newServer(t, "amateurlapdancer.com", map[string]string{"1": fixture(t, "al_page2.json")}, "1", rec)
		scenes, errs, _ := run(t, s, c.url)
		if len(errs) != 0 || len(scenes) != 1 {
			t.Fatalf("%s: scenes=%d errs=%v", c.url, len(scenes), errs)
		}
		if scenes[0].Scene.StudioURL != c.url {
			t.Errorf("StudioURL = %q", scenes[0].Scene.StudioURL)
		}
		if len(rec.posts) != 1 || !strings.Contains(rec.posts[0], c.want) {
			t.Errorf("%s: posts query %v, want %s", c.url, rec.posts, c.want)
		}
	}
}

func TestUnknownCategoryFails(t *testing.T) {
	rec := &recorder{}
	s, _ := newServer(t, "amateurlapdancer.com", nil, "1", rec)
	scenes, errs, _ := run(t, s, "https://amateurlapdancer.com/content/category/nope/")
	if len(scenes) != 0 || len(errs) != 1 {
		t.Fatalf("scenes=%d errs=%v", len(scenes), errs)
	}
	if len(rec.posts) != 0 {
		t.Errorf("posts fetched despite unresolved category: %v", rec.posts)
	}
}

func TestMatchesURL(t *testing.T) {
	byID := map[string]*Scraper{}
	for _, cfg := range sites {
		byID[cfg.ID] = New(cfg)
	}
	cases := []struct {
		id   string
		url  string
		want bool
	}{
		{"amateurlapdancer", "https://amateurlapdancer.com/", true},
		{"amateurlapdancer", "https://www.amateurlapdancer.com/content/", true},
		{"amateurlapdancer", "http://amateurlapdancer.com", true},
		{"amateurlapdancer", "https://amateurlapdancer.com/content/category/membervideos/", true},
		{"amateurlapdancer", "https://onlyfacials.com/", false},
		{"amateurlapdancer", "https://notamateurlapdancer.com/", false},
		{"onlyfacials", "https://onlyfacials.com/content/tag/bj/", true},
		{"onlyfacials", "https://genuinefeatures.com/", false},
		{"genuinefeatures", "https://genuinefeatures.com/content/", true},
		{"genuinefeatures", "https://genuinefeatures.com.evil.example/", false},
	}
	for _, c := range cases {
		if got := byID[c.id].MatchesURL(c.url); got != c.want {
			t.Errorf("%s.MatchesURL(%q) = %v, want %v", c.id, c.url, got, c.want)
		}
	}
}

func TestIsVideo(t *testing.T) {
	cases := map[string]bool{
		`<p>x<div class='play_box'>`:                           true,
		`<p><div class='join_block'></div>`:                    true,
		`<a><img src="a.jpg" class="poster"></a>`:              true,
		`<a><img decoding="async" src="a.jpg" class="poster">`: true,
		`<p><a href="x.jpg"><img src="x_thumb.jpg"></a></p>`:   false,
	}
	for in, want := range cases {
		if got := isVideo(in); got != want {
			t.Errorf("isVideo(%q) = %v, want %v", in, got, want)
		}
	}
}
