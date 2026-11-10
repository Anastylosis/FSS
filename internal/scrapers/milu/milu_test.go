package milu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func posts(t *testing.T) []post {
	t.Helper()
	b, err := os.ReadFile("testdata/posts.json")
	if err != nil {
		t.Fatal(err)
	}
	var ps []post
	if err := json.Unmarshal(b, &ps); err != nil {
		t.Fatal(err)
	}
	return ps
}

var cats = map[int]string{1: "All Item", 5: "尻フェチ", 6: "脚フェチ", 28: "コスプレ"}

func TestToSceneRelease(t *testing.T) {
	sc, ok := toScene(posts(t)[0], cats, "http://www.milu-av.com/", time.Now())
	if !ok {
		t.Fatal("release skipped")
	}
	if sc.ID != "MIBB-065" || sc.Director != "guess" || sc.Duration != 124*60 {
		t.Errorf("ID/Director/Duration = %q %q %d", sc.ID, sc.Director, sc.Duration)
	}
	if len(sc.Performers) != 2 || sc.Performers[0] != "逢沢みゆ" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if !sc.Date.Equal(time.Date(2025, 11, 18, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date = %v", sc.Date)
	}
	if !strings.HasSuffix(sc.Thumbnail, "MIBB-065.jpg") || !strings.HasPrefix(sc.Description, "小悪魔") {
		t.Errorf("Thumbnail/Description = %q / %q", sc.Thumbnail, sc.Description)
	}
}

func TestToSceneSkipsEventPost(t *testing.T) {
	if _, ok := toScene(posts(t)[1], cats, "http://www.milu-av.com/", time.Now()); ok {
		t.Error("event announcement was kept")
	}
}

func TestToSceneLegacyPost(t *testing.T) {
	sc, ok := toScene(posts(t)[3], cats, "http://www.milu-av.com/", time.Now())
	if !ok {
		t.Fatal("legacy post skipped")
	}
	if sc.ID != "268" || len(sc.Tags) == 0 || sc.Title == "" || sc.Description == "" {
		t.Errorf("scene = %+v", sc)
	}
}

func TestCategoryURLFiltersPosts(t *testing.T) {
	body, err := os.ReadFile("testdata/posts.json")
	if err != nil {
		t.Fatal(err)
	}
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("rest_route") {
		case "/wp/v2/categories":
			_, _ = w.Write([]byte(`[{"id":5,"name":"尻フェチ"}]`))
		case "/wp/v2/posts":
			queries = append(queries, r.URL.RawQuery)
			w.Header().Set("X-WP-Total", "4")
			w.Header().Set("X-WP-TotalPages", "1")
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, _ := New().ListScenes(context.Background(), "http://www.milu-av.com/?cat=48", scraper.ListOpts{})
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
		case scraper.KindError:
			t.Fatal(r.Err)
		}
	}
	if n != 3 {
		t.Errorf("scenes = %d, want 3 (event post dropped)", n)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "categories=48") {
		t.Errorf("queries = %v", queries)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"http://www.milu-av.com/", "http://milu-av.com/?cat=48"} {
		if !s.MatchesURL(u) {
			t.Errorf("no match: %s", u)
		}
	}
	if s.MatchesURL("https://example.com/") {
		t.Error("matched foreign host")
	}
}
