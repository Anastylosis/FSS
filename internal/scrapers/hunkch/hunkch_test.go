package hunkch

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
	if len(cards) != 3 || total != 93 {
		t.Fatalf("cards=%d total=%d", len(cards), total)
	}
	c := cards[0]
	if c.code != "TR-HO054" || c.brand != "TRANCE" || c.title != "働く男達 part54 ～性処理道具の新人販売員～" {
		t.Errorf("card: %+v", c)
	}
	if c.price != 2480 || c.date.Format("2006-01-02") != "2026-06-09" {
		t.Errorf("price/date: %v %v", c.price, c.date)
	}
	if !strings.HasSuffix(c.thumb, "/video/img/tr/tr-ho054/tr-ho054_pickup01.jpg") || c.desc == "" {
		t.Errorf("thumb %q desc %q", c.thumb, c.desc)
	}
}

func TestApplyDetail(t *testing.T) {
	cards, _ := parseList(string(read(t, "list.html")))
	sc := cards[0].scene("https://www.hunk-ch.com/search.php?b=234", time.Now())
	applyDetail(&sc, string(read(t, "detail.html")))
	if sc.Duration != 33*60 || sc.Studio != "TRANCE" {
		t.Errorf("duration %d studio %q", sc.Duration, sc.Studio)
	}
	if len(sc.Tags) < 4 || sc.Tags[0] != "カラミ" {
		t.Errorf("tags %v", sc.Tags)
	}
	if !strings.Contains(sc.Description, "\n") {
		t.Errorf("description lost its paragraphs: %q", sc.Description)
	}
}

func TestSearchQuery(t *testing.T) {
	q, err := searchQuery("https://www.trance-video.com/")
	if err != nil || q.Get("b") != "234" {
		t.Errorf("trance-video: %v %v", q, err)
	}
	q, err = searchQuery("https://www.hunk-ch.com/search.php?b=900&c=2")
	if err != nil || q.Get("b") != "900" || q.Get("c") != "2" {
		t.Errorf("brand: %v %v", q, err)
	}
	if _, err = searchQuery("https://www.hunk-ch.com/"); err == nil {
		t.Error("the bare marketplace root should be refused")
	}
	u := pageURL(q, 3)
	for _, want := range []string{"p=3", "b=900", "c=2", "search_flag=movie"} {
		if !strings.Contains(u, want) {
			t.Errorf("pageURL %q lacks %q", u, want)
		}
	}
}

func TestEndToEnd(t *testing.T) {
	list, detail := read(t, "list.html"), read(t, "detail.html")
	var mu sync.Mutex
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie_detail.php":
			_, _ = w.Write(detail)
		case "/search.php":
			mu.Lock()
			pages = append(pages, r.URL.Query().Get("p")+"/"+r.URL.Query().Get("b"))
			mu.Unlock()
			if r.URL.Query().Get("p") == "1" {
				_, _ = w.Write(list)
				return
			}
			_, _ = w.Write([]byte("<html></html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	s := New()
	s.client = srv.Client()
	// The listing claims 93 results at 20 per page; page 2 answers empty and ends the walk.
	ch, err := s.ListScenes(context.Background(), "https://www.trance-video.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
			testutil.ValidateScene(t, r.Scene)
			if r.Scene.Duration == 0 {
				t.Errorf("%s has no duration", r.Scene.ID)
			}
		case scraper.KindError:
			t.Fatalf("error: %v", r.Err)
		}
	}
	if n != 3 {
		t.Fatalf("got %d scenes", n)
	}
	if len(pages) != 2 || pages[0] != "1/234" || pages[1] != "2/234" {
		t.Errorf("listing requests: %v", pages)
	}
}

func TestListScenesRefusesMarketplaceRoot(t *testing.T) {
	if _, err := New().ListScenes(context.Background(), "https://www.hunk-ch.com/", scraper.ListOpts{}); err == nil {
		t.Error("expected an error")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://www.hunk-ch.com/search.php?b=234", "https://trance-video.com/", "http://www.trance-video.com/"} {
		if !s.MatchesURL(u) {
			t.Errorf("%q should match", u)
		}
	}
	if s.MatchesURL("https://hunk-ch.com.evil.test/") {
		t.Error("lookalike matched")
	}
}
