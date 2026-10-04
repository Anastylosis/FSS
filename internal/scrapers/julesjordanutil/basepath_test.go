package julesjordanutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestCategoryName(t *testing.T) {
	cases := map[string]string{
		"https://www.x.com/tour/categories/movies.html":          "movies",
		"https://www.x.com/tour/categories/movies_3_d.html":      "movies",
		"https://www.x.com/tour/categories/HairyGuys.html":       "HairyGuys",
		"https://www.x.com/tour/categories/HairyGuys_2_d.html":   "HairyGuys",
		"https://www.x.com/tour/categories/Two_Words_5_d.html":   "Two_Words",
		"https://www.x.com/tour/":                                "movies",
		"https://www.x.com/tour/categories/HairyGuys.html?x=1#f": "HairyGuys",
	}
	for u, want := range cases {
		if got := categoryName(u); got != want {
			t.Errorf("categoryName(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestClassicTourCategoryModeAndThumbs(t *testing.T) {
	listing, err := os.ReadFile("testdata/islandstuds_listing.html")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := os.ReadFile("testdata/islandstuds_detail.html")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var paths []string
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/tour/categories/HairyGuys_1_d.html", "/tour/categories/movies_1_d.html":
			_, _ = w.Write([]byte(strings.ReplaceAll(string(listing), "{{BASE}}", ts.URL+"/tour")))
		default:
			if strings.HasPrefix(r.URL.Path, "/tour/scenes/") {
				_, _ = w.Write(detail)
				return
			}
			_, _ = fmt.Fprint(w, "<div>empty</div>")
		}
	}))
	defer ts.Close()

	s := &Scraper{
		client:     ts.Client(),
		base:       ts.URL + "/tour",
		pathPrefix: "/tour",
		cfg:        SiteConfig{SiteID: "islandstuds", Domain: "islandstuds.com", StudioName: "Island Studs", Template: TemplateClassic, BasePath: "/tour"},
	}
	modes := []struct{ url, listing string }{
		{ts.URL + "/tour/categories/movies.html", "/tour/categories/movies_1_d.html"},
		{ts.URL + "/tour/categories/HairyGuys.html", "/tour/categories/HairyGuys_1_d.html"},
	}
	for _, mode := range modes {
		mu.Lock()
		paths = nil
		mu.Unlock()
		ch, err := s.ListScenes(context.Background(), mode.url, scraper.ListOpts{Delay: time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		results := testutil.CollectScenes(t, ch)
		if len(results) != 2 {
			t.Fatalf("%s: got %d scenes, want 2", mode.url, len(results))
		}
		mu.Lock()
		found := false
		for _, p := range paths {
			found = found || p == mode.listing
		}
		mu.Unlock()
		if !found {
			t.Errorf("%s: listing %s not requested", mode.url, mode.listing)
		}
		sc := results[0]
		for _, r := range results {
			if r.ID == "Lennon-Daddy-Lover" {
				sc = r
			}
		}
		if sc.ID != "Lennon-Daddy-Lover" || sc.Title != "Lennon - Daddy Lover!" {
			t.Errorf("id/title = %q / %q", sc.ID, sc.Title)
		}
		if sc.Date.Format("2006-01-02") != "2026-10-13" {
			t.Errorf("date = %v", sc.Date)
		}
		if len(sc.Performers) != 1 || sc.Performers[0] != "Lennon" {
			t.Errorf("performers = %v", sc.Performers)
		}
		if sc.StudioURL != mode.url || sc.Studio != "Island Studs" || sc.SiteID != "islandstuds" {
			t.Errorf("studioURL/studio/site = %q / %q / %q", sc.StudioURL, sc.Studio, sc.SiteID)
		}
		if !strings.HasPrefix(sc.URL, ts.URL+"/tour/scenes/") {
			t.Errorf("URL = %q", sc.URL)
		}
		if sc.Thumbnail != ts.URL+"/tour/content//contentthumbs/84/67/8467-1x.jpg" {
			t.Errorf("thumbnail = %q", sc.Thumbnail)
		}
		if !strings.HasPrefix(sc.Description, "Totally Hair Proudly Out Lennon") || strings.Contains(sc.Description, "<p>") {
			t.Errorf("description = %q", sc.Description)
		}
		if len(sc.Tags) == 0 || sc.Tags[0] != "All-American" {
			t.Errorf("tags = %v", sc.Tags)
		}
	}
}
