package maryjane

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

func TestParseList(t *testing.T) {
	b, err := os.ReadFile("testdata/shouhinlist.html")
	if err != nil {
		t.Fatal(err)
	}
	scenes := parseList(string(b), "http://mary-jane.biz/", "http://x/l", time.Now())
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "MJAD409" || sc.Title != "小女ラムネ 第6話甜花と内緒のレッスン" {
		t.Errorf("first scene: %q %q", sc.ID, sc.Title)
	}
	if sc.Date.Format("2006-01-02") != "2025-09-05" {
		t.Errorf("date %v", sc.Date)
	}
	if sc.LowestPrice != 4400 {
		t.Errorf("price %v", sc.LowestPrice)
	}
	if sc.Thumbnail != "http://mary-jane.biz/mysite1/image/ramu6jk.jpg" {
		t.Errorf("thumb %q", sc.Thumbnail)
	}
	if scenes[1].ID != "MJAD403" || scenes[1].Thumbnail == "" {
		t.Errorf("second scene: %+v", scenes[1])
	}
}

func withServer(t *testing.T, body []byte) *Scraper {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case listPath:
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = old })
	s := New()
	s.client = srv.Client()
	return s
}

func TestEndToEnd(t *testing.T) {
	b, _ := os.ReadFile("testdata/shouhinlist.html")
	s := withServer(t, b)
	ch, err := s.ListScenes(context.Background(), "http://mary-jane.biz/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
			testutil.ValidateScene(t, r.Scene)
		case scraper.KindError:
			t.Fatalf("error: %v", r.Err)
		}
	}
	if n != 4 {
		t.Fatalf("got %d scenes", n)
	}
}

func TestEmptyPageIsAParseError(t *testing.T) {
	s := withServer(t, []byte("<html></html>"))
	ch, _ := s.ListScenes(context.Background(), "http://mary-jane.biz/", scraper.ListOpts{})
	var gotErr bool
	for r := range ch {
		if r.Kind == scraper.KindError && scraper.Classify(r.Err) == scraper.FailureParse {
			gotErr = true
		}
	}
	if !gotErr {
		t.Fatal("no parse error")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"http://mary-jane.biz/", "https://www.mary-jane.biz/mysite1/shouhinlist.html"} {
		if !s.MatchesURL(u) {
			t.Errorf("%q should match", u)
		}
	}
	if s.MatchesURL("https://mary-jane.biz.example.com/") {
		t.Error("lookalike matched")
	}
}
