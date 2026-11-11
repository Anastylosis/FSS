package tiedvirgins

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://tiedvirgins.com/updates.html": true,
		"https://www.tiedvirgins.com/":         true,
		"https://example.com/":                 false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v", u, got)
		}
	}
}

func TestParsePage(t *testing.T) {
	scenes := parsePage(fixture(t, "page1.html"), "https://tiedvirgins.com/updates.html", "https://tiedvirgins.com/updates.html", time.Now())
	if len(scenes) != 2 {
		t.Fatalf("got %d entries", len(scenes))
	}
	sc := scenes[0]
	if sc.Title != "Busty Secretary Mia is bound to her chair!" || sc.ID != "2010-09-06-busty-secretary-mia-is-bound-to-her-chair" ||
		!sc.Date.Equal(time.Date(2010, 9, 6, 0, 0, 0, 0, time.UTC)) ||
		sc.Thumbnail != siteBase+"/awizicon/91_1.jpg" || !strings.HasPrefix(sc.Description, "Fulfilling all our naughty secretary") {
		t.Errorf("scene = %+v", sc)
	}
	if scenes[1].Thumbnail != siteBase+"/awizicon/90_1.jpg" {
		t.Errorf("second thumbnail = %q", scenes[1].Thumbnail)
	}
}

func TestParsePageMismatchedImageNumbers(t *testing.T) {
	scenes := parsePage(fixture(t, "page2.html"), "u", "u", time.Now())
	if len(scenes) != 3 || scenes[1].Title != "Cross Tied Slut Bag" || scenes[1].Thumbnail != siteBase+"/awizicon/1825_1.jpg" {
		t.Errorf("scenes = %+v", scenes)
	}
}

func TestListScenesSkipsHolesAndDuplicates(t *testing.T) {
	var requested []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		switch r.URL.Path {
		case "/updates.html":
			_, _ = w.Write([]byte(fixture(t, "page1.html")))
		case "/updates3.html":
			_, _ = w.Write([]byte(fixture(t, "page2.html")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://tiedvirgins.com/updates.html", scraper.ListOpts{})
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
	if n != 4 {
		t.Errorf("got %d scenes, want 4 (page 3 repeats one of page 1's entries; page 2 is a hole)", n)
	}
	if want := 3 + maxGap; len(requested) != want {
		t.Errorf("requested %d pages: %v", len(requested), requested)
	}
}
