package rocksboys

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/scenes.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestToScene(t *testing.T) {
	var items []apiScene
	if err := json.Unmarshal(fixture(t), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	sc := toScene(items[0], "https://www.rocksboys.com", time.Now())
	if sc.ID != "14689" || sc.Title != "Lost & Found - Nick Wild" || sc.Date.Format("2006-01-02") != "2026-11-20" {
		t.Errorf("scene = %+v", sc)
	}
	if sc.Duration != 24*60+25 || len(sc.Performers) != 1 || sc.Performers[0] != "Nick WIld" {
		t.Errorf("Duration/Performers = %d %v", sc.Duration, sc.Performers)
	}
	if sc.URL != siteBase+"/scene/14689" || sc.Thumbnail == "" || sc.Description == "" {
		t.Errorf("URL/Thumbnail/Description = %q %q %q", sc.URL, sc.Thumbnail, sc.Description)
	}
}

func TestListScenesSubmitsAgeGate(t *testing.T) {
	body := fixture(t)
	var gotReturn string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			if _, err := r.Cookie("age_verified"); err == nil {
				_, _ = w.Write([]byte("home"))
				return
			}
			_, _ = w.Write([]byte(`<form method="POST" action="/age-gate/confirm" class="ag-form">
<input type="hidden" name="_token" value="tok123" autocomplete="off"><input type="hidden" name="return" value="/">
<button type="submit">Enter</button></form>`))
		case "/age-gate/confirm":
			if r.Method != http.MethodPost || r.FormValue("_token") != "tok123" {
				http.Error(w, "bad token", http.StatusForbidden)
				return
			}
			gotReturn = r.FormValue("return")
			http.SetCookie(w, &http.Cookie{Name: "age_verified", Value: "1", Path: "/"})
			http.Redirect(w, r, "/", http.StatusFound)
		case "/ajax/scenes":
			if r.URL.Query().Get("offset") != "0" || r.URL.Query().Get("limit") != "50" {
				t.Errorf("query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://www.rocksboys.com", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatal(r.Err)
		case scraper.KindScene:
			n++
		}
	}
	if n != 3 {
		t.Errorf("scenes = %d, want 3", n)
	}
	if gotReturn != "/" {
		t.Errorf("hidden return field not submitted: %q", gotReturn)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://www.rocksboys.com/":       true,
		"https://rocksboys.com/scenes":     true,
		"https://notrocksboys.com/":        false,
		"https://www.rocksboys.com.evil/x": false,
	} {
		if s.MatchesURL(u) != want {
			t.Errorf("MatchesURL(%q) != %v", u, want)
		}
	}
}
