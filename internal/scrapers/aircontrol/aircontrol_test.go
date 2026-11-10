package aircontrol

import (
	"context"
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

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseList(t *testing.T) {
	cards, total := parseList(string(read(t, "list.html")))
	if len(cards) != 3 || total != 246 {
		t.Fatalf("cards=%d total=%d", len(cards), total)
	}
	if cards[0].id != "oae324" || cards[0].title != "ALL NUDE" || cards[0].label != "antenna" {
		t.Errorf("card: %+v", cards[0])
	}
	if !strings.HasPrefix(cards[0].thumb, "https://www.i-dol.tv/contents/works/oae324/") {
		t.Errorf("thumb %q", cards[0].thumb)
	}
}

func TestApplyDetail(t *testing.T) {
	sc := (card{id: "ome721", title: "x"}).scene("https://www.i-dol.tv/", time.Now())
	applyDetail(&sc, string(read(t, "detail.html")))
	if sc.Title != "嫌がるわりには従順なキミ" || sc.Date.Format("2006-01-02") != "2026-10-27" || sc.Duration != 7200 {
		t.Errorf("scene: %q %v %d", sc.Title, sc.Date, sc.Duration)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "夏来唯" || sc.Series != "MeLON" {
		t.Errorf("performers %v series %q", sc.Performers, sc.Series)
	}
	if len(sc.Tags) != 5 || !strings.Contains(sc.Description, "官能的") || sc.Preview == "" {
		t.Errorf("tags %v desc %q preview %q", sc.Tags, sc.Description, sc.Preview)
	}
}

func TestLabelsFor(t *testing.T) {
	if got := labelsFor("https://www.i-dol.tv/works/list/label/2/"); len(got) != 1 || got[0] != "2" {
		t.Errorf("label URL: %v", got)
	}
	if got := labelsFor("https://www.i-dol.tv/"); len(got) != len(labelIDs) {
		t.Errorf("root: %v", got)
	}
}

func TestEndToEnd(t *testing.T) {
	list, detail := read(t, "list.html"), read(t, "detail.html")
	var mu sync.Mutex
	listHits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/works/detail/"):
			_, _ = w.Write(detail)
		case r.URL.Path == "/works/list/label/2/1/":
			mu.Lock()
			listHits[r.URL.Path]++
			mu.Unlock()
			_, _ = w.Write(list)
		default:
			_, _ = w.Write([]byte("<html></html>"))
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	s := New()
	s.client = srv.Client()
	ch, err := s.ListScenes(context.Background(), "https://www.i-dol.tv/works/list/label/2/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
			testutil.ValidateScene(t, r.Scene)
			if r.Scene.Date.IsZero() || len(r.Scene.Performers) == 0 {
				t.Errorf("%s lacks detail fields", r.Scene.ID)
			}
		case scraper.KindError:
			t.Fatalf("error: %v", r.Err)
		}
	}
	if n != 3 {
		t.Fatalf("got %d scenes", n)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("https://www.i-dol.tv/works/list/label/1/") || s.MatchesURL("https://i-dol.tv.evil.test/") {
		t.Error("MatchesURL wrong")
	}
}
