package salierixxx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/list.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParse(t *testing.T) {
	scenes := New().parse(fixture(t), "https://salierixxx.com/en/", time.Now())
	if len(scenes) != 3 {
		t.Fatalf("scenes = %d, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "8144" || sc.Title != "What a pleasure to watch" || sc.Date.Format("2006-01-02") != "2026-11-19" {
		t.Errorf("scene = %+v", sc)
	}
	if len(sc.Performers) != 2 || sc.Performers[0] != "Violet Voss" || sc.Performers[1] != "James Hardon" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Thumbnail == "" || sc.URL != siteBase+"/en/#!pages/video/8144/guardare_che_piacere" {
		t.Errorf("Thumbnail/URL = %q %q", sc.Thumbnail, sc.URL)
	}
}

func TestListScenesConfirmsAge(t *testing.T) {
	body := fixture(t)
	var filters []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/en/disclaimer.php":
			if r.URL.Query().Get("accept") == "tok" {
				http.SetCookie(w, &http.Cookie{Name: "ok", Value: "1", Path: "/"})
				http.Redirect(w, r, "/en/", http.StatusFound)
				return
			}
			_, _ = w.Write([]byte(`<a href="?accept=tok" id="enter">ENTER</a>`))
		case "/en/pages/section/video/":
			if c, err := r.Cookie("ok"); err != nil || c.Value != "1" {
				http.Redirect(w, r, "/en/disclaimer.php", http.StatusFound)
				return
			}
			filters = append(filters, r.URL.RawQuery)
			_, _ = w.Write([]byte(body))
		default:
			_, _ = w.Write([]byte("home"))
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://salierixxx.com/en/#!pages/section/video/?genere=21", scraper.ListOpts{})
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
	if len(filters) != 1 || filters[0] != "genere=21&page=1" {
		t.Errorf("listing queries = %v", filters)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://salierixxx.com/en/":    true,
		"https://www.salierixxx.com":    true,
		"https://salierixxx.org/en/":    false,
		"https://notsalierixxx.com/en/": false,
	} {
		if s.MatchesURL(u) != want {
			t.Errorf("MatchesURL(%q) != %v", u, want)
		}
	}
}
