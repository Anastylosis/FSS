package hibino

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseListAndDetail(t *testing.T) {
	items := parseList(string(read(t, "list.html")))
	if len(items) != 2 {
		t.Fatalf("got %d items", len(items))
	}
	sc := items[0].scene("https://av-hibino.com/", time.Now())
	if sc.ID != "HBAD-745" || sc.Duration != 127*60 || sc.Director != "六反園正史" {
		t.Errorf("scene: %+v", sc)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "一色さら" {
		t.Errorf("performers %v", sc.Performers)
	}
	if sc.Date.Format("2006-01-02") != "2026-11-05" {
		t.Errorf("date %v", sc.Date)
	}
	if sc.Thumbnail == "" || items[0].wrID != "104" {
		t.Errorf("thumb/wr_id: %q %q", sc.Thumbnail, items[0].wrID)
	}
	desc, cover := parseDetail(string(read(t, "detail.html")))
	if desc == "" || cover == "" {
		t.Errorf("detail: %q %q", desc, cover)
	}
}

func TestEndToEnd(t *testing.T) {
	list, detail := read(t, "list.html"), read(t, "detail.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path != "/web/bbs/board.php":
			http.NotFound(w, r)
		case r.URL.Query().Get("wr_id") != "":
			_, _ = w.Write(detail)
		case r.URL.Query().Get("page") == "1":
			_, _ = w.Write(list)
		default:
			_, _ = w.Write([]byte("<html><body><ul id=\"gall_ul\"></ul></body></html>"))
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	s := New()
	s.client = srv.Client()
	ch, err := s.ListScenes(context.Background(), "https://av-hibino.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
			testutil.ValidateScene(t, r.Scene)
			if r.Scene.Description == "" {
				t.Errorf("%s has no description", r.Scene.ID)
			}
		case scraper.KindError:
			t.Fatalf("error: %v", r.Err)
		}
	}
	if n != 2 {
		t.Fatalf("got %d scenes", n)
	}
}

func TestDateToleratesStrayZero(t *testing.T) {
	it := item{wrID: "1", fields: map[string]string{"発売日": "2026/010/08", "品番": "X-1"}}
	if d := it.scene("u", time.Now()).Date.Format("2006-01-02"); d != "2026-10-08" {
		t.Errorf("date %s", d)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("https://av-hibino.com/web/bbs/board.php?bo_table=works") || s.MatchesURL("https://av-hibino.com.evil.test/") {
		t.Error("MatchesURL wrong")
	}
}
