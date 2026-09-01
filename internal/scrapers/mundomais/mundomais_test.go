package mundomais

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://www.mundomais.com.br/", "https://mundomais.com.br/galerias/videos", "http://mundomais.com.br"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://mundomais.com/", "https://notmundomais.com.br/", "https://example.com/mundomais.com.br/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

// The site is served as ISO-8859-1 and the titles are Portuguese; reading the
// bytes as UTF-8 mangles roughly every other one.
func TestDecodeLatin1(t *testing.T) {
	raw := []byte{'P', 'o', 'l', 'i', 'c', 'i', 'a', 'l', ' ', 0xE0, ' ', 'p', 'a', 'i', 's', 'a', 'n', 'a'}
	if got := string(decodeLatin1(raw)); got != "Policial à paisana" {
		t.Errorf("decodeLatin1 = %q", got)
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(decodeLatin1(readFixture(t, "listing_page1.html")))
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	if first.id == "" || !strings.Contains(first.path, "/video"+first.id+"-") {
		t.Errorf("id/path = %q/%q", first.id, first.path)
	}
	if first.title == "" {
		t.Error("title missing")
	}
	if strings.ContainsRune(first.title, '�') {
		t.Errorf("title %q was mis-decoded", first.title)
	}
	if first.description == "" {
		t.Error("description missing")
	}
	if first.date == "" {
		t.Errorf("date = %q", first.date)
	}
	if !strings.HasSuffix(first.preview, ".mp4") {
		t.Errorf("preview = %q", first.preview)
	}
}

func TestMaxPage(t *testing.T) {
	if got := maxPage(decodeLatin1(readFixture(t, "listing_page1.html"))); got != 101 {
		t.Errorf("maxPage = %d, want 101", got)
	}
	if got := maxPage([]byte("<html></html>")); got != 0 {
		t.Errorf("maxPage = %d, want 0", got)
	}
}

func TestToScene(t *testing.T) {
	s := New()
	item := listItem{
		id: "25195", path: "/galerias/videos/netvideos/video25195-x", title: "T",
		description: "D", thumbnail: "/mundohot/filmes/25195/foto-16x9.jpg",
		preview: "/mundohot/filmes/25195/preview.mp4", date: "2026-08-27", views: 7,
	}
	sc := s.toScene(item, "https://www.mundomais.com.br/", time.Now().UTC())
	if sc.URL != "https://www.mundomais.com.br/galerias/videos/netvideos/video25195-x" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Thumbnail != "https://www.mundomais.com.br/mundohot/filmes/25195/foto-16x9.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.Date.Format("2006-01-02") != "2026-08-27" || sc.Views != 7 {
		t.Errorf("Date/Views = %v/%d", sc.Date, sc.Views)
	}

	item.date = "not a date"
	if got := s.toScene(item, "https://www.mundomais.com.br/", time.Now().UTC()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

func TestListScenes(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Path)
		// Page 2 and beyond re-serve page 1: the fixture's pager names 101, so
		// the walk must stop on the repeat rather than run to that count.
		_, _ = w.Write(readFixture(t, "listing_page1.html"))
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://www.mundomais.com.br/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []models.Scene
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindError:
			t.Errorf("error result: %v", res.Err)
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 — the repeats must be deduplicated", len(scenes))
	}
	if len(pages) > 4 {
		t.Errorf("fetched %d pages of repeats", len(pages))
	}
	for _, sc := range scenes {
		if !strings.HasPrefix(sc.URL, srv.URL) {
			t.Errorf("URL = %q, want it under the test server", sc.URL)
		}
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}
