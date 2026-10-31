package avamoore

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
	b, err := os.ReadFile("testdata/page.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestToScene(t *testing.T) {
	var l listing
	if err := json.Unmarshal(fixture(t), &l); err != nil {
		t.Fatal(err)
	}
	if len(l.Data.Data) == 0 {
		t.Fatal("no videos")
	}
	sc := toScene(l.Data.Data[0], "https://www.ava-moore.com/videos", time.Now())
	if sc.ID == "" || sc.Title == "" || sc.Description == "" || sc.Date.IsZero() || sc.Duration <= 0 {
		t.Errorf("scene = %+v", sc)
	}
	if sc.URL != "https://www.ava-moore.com/videos/"+l.Data.Data[0].Slug {
		t.Errorf("URL = %q", sc.URL)
	}
}

func TestListScenes(t *testing.T) {
	body := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/videos-pornos":
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`{"data":{"last_page":18,"data":[]}}`))
				return
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

	s := New()
	ch, err := s.ListScenes(context.Background(), "https://www.ava-moore.com/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		if r.Kind == scraper.KindError {
			t.Fatal(r.Err)
		}
		if r.Kind == scraper.KindScene {
			n++
		}
	}
	if n != 3 {
		t.Errorf("scenes = %d, want 3", n)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://www.ava-moore.com/videos": true,
		"https://ava-moore.com/":           true,
		"https://example.com/":             false,
	} {
		if s.MatchesURL(u) != want {
			t.Errorf("MatchesURL(%q) != %v", u, want)
		}
	}
}
