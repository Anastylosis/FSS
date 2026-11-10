package a1c

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

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseMonthOnlyDate(t *testing.T) {
	scenes := parse(read(t, "majin.html"), "https://www.a1c.jp/~majin/", "https://www.a1c.jp/~majin/", time.Now())
	if len(scenes) != 4 {
		t.Fatalf("scenes = %d", len(scenes))
	}
	sc := scenes[1]
	if sc.ID != "ntr_knight_02" || !strings.HasPrefix(sc.Title, "NTR騎士 第２話") && !strings.HasPrefix(sc.Title, "NTR騎士　第２話") {
		t.Errorf("ID/Title = %q / %q", sc.ID, sc.Title)
	}
	if !sc.Date.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.Studio != "魔人" || sc.URL != "https://www.a1c.jp/~majin/product/ntr_knight_02.html" ||
		sc.Thumbnail != "https://www.a1c.jp/~majin/product/ntr_knight_02/proban_ACJDP0100.jpg" {
		t.Errorf("scene = %+v", sc)
	}
}

func TestParseDayDateAndCommentedItem(t *testing.T) {
	scenes := parse(read(t, "chuchu.html"), "https://www.a1c.jp/~chuchu/", "u", time.Now())
	if len(scenes) != 2 {
		t.Fatalf("scenes = %d (commented-out item must be ignored)", len(scenes))
	}
	if !scenes[0].Date.Equal(time.Date(2016, 5, 27, 0, 0, 0, 0, time.UTC)) || scenes[0].Studio != "chuchu" {
		t.Errorf("scene = %+v", scenes[0])
	}
	if strings.Contains(scenes[0].Title, "Release") {
		t.Errorf("Title = %q", scenes[0].Title)
	}
}

func TestParseBrandpageLayout(t *testing.T) {
	scenes := parse(read(t, "mousousenka.html"), "https://www.a1c.jp/brandpage/mousousenka/", "u", time.Now())
	if len(scenes) != 3 || scenes[0].Studio != "妄想専科" || scenes[2].Date.Day() != 28 {
		t.Errorf("scenes = %+v", scenes)
	}
}

func TestListScenes(t *testing.T) {
	body := read(t, "majin.html")
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, _ := New().ListScenes(context.Background(), "https://www.a1c.jp/~majin/top.html", scraper.ListOpts{})
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
			if !strings.HasPrefix(r.Scene.URL, srv.URL) {
				t.Errorf("URL = %s", r.Scene.URL)
			}
		case scraper.KindError:
			t.Fatal(r.Err)
		}
	}
	if n != 4 || path != "/~majin/product.html" {
		t.Errorf("scenes = %d, path = %s", n, path)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://www.a1c.jp/~majin/top.html", "http://www.a1c.jp/~chuchu/product.html", "https://www.a1c.jp/brandpage/mousousenka/top.html"} {
		if !s.MatchesURL(u) {
			t.Errorf("no match: %s", u)
		}
	}
	if s.MatchesURL("https://www.a1c.jp/") || s.MatchesURL("https://example.com/~x/") {
		t.Error("matched a non-label URL")
	}
}
