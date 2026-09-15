package tma

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.tma.co.jp",
		"http://www.tma.co.jp/",
		"https://tma.co.jp/collections/vr",
		"https://www.tma.co.jp/products/fays-017",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://tma.co.uk/", "https://example.com/tma.co.jp", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestToScene(t *testing.T) {
	p := product{
		Title:       " 作品タイトル ",
		Handle:      "fays-017",
		BodyHTML:    "<p>説明 &amp; more</p>",
		PublishedAt: "2026-08-27T14:31:49+09:00",
		Vendor:      "I.B.WORKS",
		ProductType: "VR",
		Tags:        []string{"春咲結菜", "巨乳"},
		Variants: []struct {
			Price string `json:"price"`
		}{{Price: "4180"}},
		Images: []struct {
			Src string `json:"src"`
		}{{Src: "https://cdn.shopify.com/a.jpg"}},
	}
	scene := toScene(p, "https://www.tma.co.jp", time.Now().UTC())

	if scene.ID != "fays-017" || scene.SiteID != siteID {
		t.Errorf("ID/SiteID = %q/%q", scene.ID, scene.SiteID)
	}
	if scene.Title != "作品タイトル" {
		t.Errorf("Title = %q", scene.Title)
	}
	// The store carries sister labels, so the product's own vendor wins.
	if scene.Studio != "I.B.WORKS" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if scene.Description != "説明 & more" {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Date.Format("2006-01-02") != "2026-08-27" {
		t.Errorf("Date = %v", scene.Date)
	}
	if scene.URL != siteBase+"/products/fays-017" {
		t.Errorf("URL = %q", scene.URL)
	}
	if len(scene.PriceHistory) != 1 || scene.PriceHistory[0].Regular != 4180 {
		t.Errorf("PriceHistory = %+v", scene.PriceHistory)
	}
	if strings.Join(scene.Categories, "|") != "VR" {
		t.Errorf("Categories = %v", scene.Categories)
	}
}

func TestStudioFallsBackToTheStoreName(t *testing.T) {
	if got := studio("  "); got != studioName {
		t.Errorf("studio = %q, want %q", got, studioName)
	}
}

func productsJSON(t *testing.T, ps []product) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"products": ps})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Photo books share the storefront with the video releases and are not scenes.
func TestPhotoBooksAreSkipped(t *testing.T) {
	page := []product{
		{Title: "Video", Handle: "vid-1", ProductType: "VR", PublishedAt: "2026-01-01T00:00:00+09:00"},
		{Title: "Photos", Handle: "pic-1", ProductType: "AI写真集", PublishedAt: "2026-01-02T00:00:00+09:00"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			_, _ = fmt.Fprint(w, productsJSON(t, page))
			return
		}
		_, _ = fmt.Fprint(w, `{"products":[]}`)
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var ids []string
	for r := range ch {
		if r.Kind == scraper.KindScene {
			ids = append(ids, r.Scene.ID)
		}
	}
	if strings.Join(ids, ",") != "vid-1" {
		t.Errorf("scene ids = %v, want just the video", ids)
	}
}

// A collection URL scrapes that collection's own endpoint — which is how the
// sister labels and the VR line are scraped on their own.
func TestCollectionURLUsesTheCollectionEndpoint(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = fmt.Fprint(w, `{"products":[]}`)
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	for _, tc := range []struct{ url, wantPath string }{
		{srv.URL + "/collections/ii-b-works", "/collections/ii-b-works/products.json"},
		{srv.URL + "/collections/all", "/products.json"},
		// /collections/vendors?q=… is a search page with no products.json.
		{srv.URL + "/collections/vendors?q=%E9%9D%92%E7%A9%BA", "/products.json"},
	} {
		paths = nil
		ch, err := s.ListScenes(context.Background(), tc.url, scraper.ListOpts{})
		if err != nil {
			t.Fatalf("ListScenes: %v", err)
		}
		for range ch { //nolint:revive // drain so the goroutine can finish its sends
		}
		if len(paths) == 0 || paths[0] != tc.wantPath {
			t.Errorf("%s fetched %v, want %s", tc.url, paths, tc.wantPath)
		}
	}
}

// A short page is the last one; the walk must not keep asking for more.
func TestShortPageEndsTheWalk(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		_, _ = fmt.Fprint(w, productsJSON(t, []product{{Title: "One", Handle: "one", PublishedAt: "2026-01-01T00:00:00+09:00"}}))
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	for range ch { //nolint:revive // drain
	}
	if pages != 1 {
		t.Errorf("fetched %d pages, want 1", pages)
	}
}
