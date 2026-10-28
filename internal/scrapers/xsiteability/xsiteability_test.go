package xsiteability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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

func TestParseGrid(t *testing.T) {
	cards, total := parseGrid(fixture(t, "grid.html"), "https://gkaproductions.com")
	if len(cards) != 2 || total != 2 {
		t.Fatalf("cards=%d total=%d", len(cards), total)
	}
	c := cards[0]
	if c.id != "169934" || c.duration != 12*60+36 || !strings.HasPrefix(c.title, "After the Club: Stella Elle & Ginary") {
		t.Errorf("card = %+v", c)
	}
	if !strings.HasPrefix(c.thumb, "https://gkaproductions.com/x/users/") || strings.Contains(c.thumb, " ") {
		t.Errorf("thumb = %q", c.thumb)
	}
}

func TestParseGridSkipsPhotoSets(t *testing.T) {
	block := `<li class="span4 prevgrid"><a href="/x-new/new-set.php?setid=1"><h4>Pics</h4><p>
		54 photos</p></a></li>`
	cards, total := parseGrid(block, "b")
	if len(cards) != 0 || total != 1 {
		t.Errorf("cards=%v total=%d", cards, total)
	}
}

func TestRunReadsSetPages(t *testing.T) {
	grid, set := fixture(t, "grid.html"), fixture(t, "set.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/x-new/new-preview-grid.php" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(grid))
		case r.URL.Path == "/x-new/new-preview-grid.php":
			_, _ = w.Write([]byte("<html></html>"))
		case r.URL.Path == "/x-new/new-set.php" && r.URL.Query().Get("setid") == "169934":
			_, _ = w.Write([]byte(set))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := New(sites[0])
	s.base = srv.URL
	s.Client = srv.Client()
	ch, _ := s.ListScenes(context.Background(), "https://gkaproductions.com/", scraper.ListOpts{})
	var got []string
	errs := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			got = append(got, r.Scene.ID)
			if r.Scene.ID == "169934" {
				if !strings.HasPrefix(r.Scene.Description, "Stella Elle and Ginary stumble") || r.Scene.LowestPrice != 11.95 {
					t.Errorf("Description = %q, price = %v", r.Scene.Description, r.Scene.LowestPrice)
				}
			}
		case scraper.KindError:
			errs++
		}
	}
	// The second set's page 404s: reported, its grid fields kept.
	if len(got) != 2 || errs != 1 {
		t.Errorf("scenes=%v errs=%d", got, errs)
	}
}
