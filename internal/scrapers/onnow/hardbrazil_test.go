package onnow_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/onnow"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func hardBrazilConfig(t *testing.T, id string) onnow.SiteConfig {
	t.Helper()
	for _, c := range onnow.Sites() {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no site %q", id)
	return onnow.SiteConfig{}
}

func TestHardBrazilListScenes(t *testing.T) {
	list1, list2 := fixture(t, "hardbrazil_list1.html"), fixture(t, "hardbrazil_list2.html")
	detail := fixture(t, "hardbrazil_detail.html")
	var mu sync.Mutex
	var listings, details []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		p := r.URL.Path
		var body string
		switch {
		case strings.HasPrefix(p, "/video/"):
			details = append(details, p)
			body = detail
		case p == "/videos":
			listings = append(listings, p)
			body = list1
		case p == "/videos/2":
			listings = append(listings, p)
			body = list2
		default:
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	cfg := hardBrazilConfig(t, "hardbrazil")
	cfg.BaseURL = srv.URL
	s := onnow.New(cfg)
	s.Client = srv.Client()

	// A known id must not end the walk: the listing carries no dates, so its
	// order cannot be trusted for an early stop.
	opts := scraper.ListOpts{KnownIDs: map[string]bool{"cf7561169eb1cab22b2624a3e9f66310": true}}
	studio := srv.URL + "/videos"
	ch, err := s.ListScenes(context.Background(), studio, opts)
	if err != nil {
		t.Fatal(err)
	}
	scenes, stopped := testutil.CollectScenesWithStop(t, ch)
	if stopped {
		t.Error("HardBrazil must not stop early on a known id")
	}
	if len(scenes) != 5 {
		t.Fatalf("got %d scenes, want 5", len(scenes))
	}
	if want := "/videos,/videos/2"; strings.Join(listings, ",") != want {
		t.Errorf("listing requests = %v, want %s", listings, want)
	}
	if len(details) != 5 {
		t.Errorf("detail fetches = %d, want 5", len(details))
	}

	sc := scenes[0]
	if sc.ID != "cf7561169eb1cab22b2624a3e9f66310" || sc.SiteID != "hardbrazil" || sc.Studio != "HardBrazil" {
		t.Errorf("ID/SiteID/Studio = %q/%q/%q", sc.ID, sc.SiteID, sc.Studio)
	}
	if sc.Title != "Ela acaba de chegar da carona e já é surpreendida no banho" {
		t.Errorf("Title = %q; the listing's truncated title must give way to the detail's", sc.Title)
	}
	if sc.Description != "Por trás da câmeras com Felina Baby" {
		t.Errorf("Description = %q", sc.Description)
	}
	if sc.Duration != 13*60+1 {
		t.Errorf("Duration = %d, want 781", sc.Duration)
	}
	if sc.URL != srv.URL+"/video/"+sc.ID || sc.StudioURL != studio {
		t.Errorf("URL/StudioURL = %q/%q", sc.URL, sc.StudioURL)
	}
	if !strings.HasPrefix(sc.Thumbnail, "https://cdn.onvicdn.com/onnowme/thumbs/cf7561169eb1cab22b2624a3e9f66310/03.jpg") {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
}

func TestHardBrazilMatchesURL(t *testing.T) {
	s := onnow.New(hardBrazilConfig(t, "hardbrazil"))
	cases := []struct {
		url  string
		want bool
	}{
		{"https://hardbrazil.com.br", true},
		{"https://hardbrazil.com.br/videos", true},
		{"https://www.hardbrazil.com.br/videos/anal-brazil", true},
		{"https://hardbrazil.com.br/videos/anal/3", true},
		{"https://hardbrazil.com.br.example.test/", false},
		{"https://irmaosdotados.com.br/videos", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}
