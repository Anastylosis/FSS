package sadisticvillage

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

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/posts.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestToScene(t *testing.T) {
	var ps []post
	if err := json.Unmarshal(fixture(t), &ps); err != nil {
		t.Fatal(err)
	}
	sc := toScene(ps[0], "https://sadisticvill.com/", time.Now())
	if sc.ID != "SVVRT-083" || sc.Duration != 162*60 {
		t.Errorf("ID/Duration = %q %d", sc.ID, sc.Duration)
	}
	if sc.Title == "" || sc.Description == "" || len(sc.Tags) == 0 || sc.Director == "" || sc.Series == "" {
		t.Errorf("scene = %+v", sc)
	}
	if sc.Date.IsZero() || sc.Date.Hour() != 0 {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.Thumbnail == "" {
		t.Errorf("no thumbnail")
	}

	old := toScene(ps[1], "https://sadisticvill.com/", time.Now())
	if old.ID != "SVVRT-019" || len(old.Performers) != 3 || old.Performers[0] != "りり" {
		t.Errorf("old = %q %v", old.ID, old.Performers)
	}
	if !strings.Contains(old.Thumbnail, "SVVRT-019") {
		t.Errorf("Thumbnail = %s", old.Thumbnail)
	}
}

func TestRun(t *testing.T) {
	body := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("categories") != "7" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("X-WP-Total", "2")
		w.Header().Set("X-WP-TotalPages", "1")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	s := New()
	s.Client = srv.Client()
	ch, err := s.ListScenes(context.Background(), "https://sadisticvill.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
		case scraper.KindError:
			t.Error(r.Err)
		}
	}
	if n != 2 {
		t.Errorf("scenes = %d", n)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("https://sadisticvill.com/") || s.MatchesURL("https://example.com/") {
		t.Error("MatchesURL")
	}
}
