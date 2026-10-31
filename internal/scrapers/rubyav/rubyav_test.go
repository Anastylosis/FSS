package rubyav

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseList(t *testing.T) {
	body := read(t, "list.html")
	items := parseList(body)
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	if items[0].code != "2TKD-074" || items[0].title == "" || items[0].thumb != siteBase+"/product/pickup/2tkd-074.jpg" {
		t.Errorf("item = %+v", items[0])
	}
	if parseTotal(body) != 2892 {
		t.Errorf("total = %d", parseTotal(body))
	}
}

func TestApplyDetail(t *testing.T) {
	sc := listScene(item{code: "2AED-213", title: "t"}, "https://www.ruby-av.com/", time.Now())
	applyDetail(&sc, read(t, "detail.html"), time.Now())
	if len(sc.Performers) != 1 || sc.Performers[0] != "嶋崎かすみ" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Studio != "ACROSS" || sc.Series != "お母さんに膣中出し" || sc.Director != "矢野口 進" {
		t.Errorf("Studio/Series/Director = %q %q %q", sc.Studio, sc.Series, sc.Director)
	}
	if sc.Duration != 7200 || !sc.Date.Equal(time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Duration/Date = %d %v", sc.Duration, sc.Date)
	}
	if sc.LowestPrice != 2550 || sc.Description == "" || len(sc.Tags) != 8 || sc.Tags[0] != "近親相姦" {
		t.Errorf("scene = %+v", sc)
	}
}

func TestListScenes(t *testing.T) {
	list, detail := read(t, "list.html"), read(t, "detail.html")
	var details atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search.php":
			if r.URL.Query().Get("p") != "1" {
				_, _ = w.Write([]byte("<html></html>"))
				return
			}
			_, _ = w.Write([]byte(list))
		case "/dvd_detail.php":
			details.Add(1)
			_, _ = w.Write([]byte(detail))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	s := New()
	opts := scraper.ListOpts{KnownIDs: map[string]bool{"2MKD-260": true}}
	ch, err := s.ListScenes(context.Background(), "https://www.ruby-av.com/", opts)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for r := range ch {
		if r.Kind == scraper.KindError {
			t.Fatal(r.Err)
		}
		if r.Kind == scraper.KindScene {
			ids = append(ids, r.Scene.ID)
		}
	}
	if len(ids) != 2 || details.Load() != 2 {
		t.Errorf("ids = %v, detail fetches = %d; want 2 scenes and 2 fetches (known one skipped)", ids, details.Load())
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://www.ruby-av.com/":                      true,
		"https://www.ruby-av.com/search.php?mkr=ACROSS": true,
		"https://example.com/":                          false,
	} {
		if s.MatchesURL(u) != want {
			t.Errorf("MatchesURL(%q) != %v", u, want)
		}
	}
}
