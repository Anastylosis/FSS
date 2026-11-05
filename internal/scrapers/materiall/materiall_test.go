package materiall

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

func detailHTML(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/detail.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseDetail(t *testing.T) {
	d := parseDetail(detailHTML(t))
	if d.Description != "おっぱいは大きい方が好き。ぷるぷるたぷたぷ&ふわふわ。" {
		t.Errorf("Description = %q", d.Description)
	}
	if !strings.HasSuffix(d.Thumbnail, "MTAFB-007_1200.jpg") {
		t.Errorf("Thumbnail = %q", d.Thumbnail)
	}
	if len(d.Performers) != 2 || d.Performers[0] != "西條るり" || d.Performers[1] != "天宮かすみ" {
		t.Errorf("Performers = %v", d.Performers)
	}
	if d.Duration != 1291*60 || !d.Date.Equal(time.Date(2026, 3, 26, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Duration/Date = %d %v", d.Duration, d.Date)
	}
}

const worksJSON = `[{"id":2159,"date_gmt":"2026-03-26T02:36:26","slug":"mtafb-007","link":"LINK","title":{"rendered":"&#8216;Title&#8217; one"},"tags":[4,24]}]`

func TestListScenes(t *testing.T) {
	page := detailHTML(t)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/works/mtafb-007/":
			_, _ = w.Write([]byte(page))
		case strings.Contains(r.URL.RawQuery, "wp/v2/tags"):
			_, _ = w.Write([]byte(`[{"id":4,"name":"巨乳"},{"id":24,"name":"4K"}]`))
		case strings.Contains(r.URL.RawQuery, "wp/v2/works"):
			w.Header().Set("X-WP-Total", "1")
			w.Header().Set("X-WP-TotalPages", "1")
			_, _ = w.Write([]byte(strings.ReplaceAll(worksJSON, "LINK", srv.URL+"/works/mtafb-007/")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	s := &Scraper{Client: srv.Client()}
	ch, err := s.ListScenes(context.Background(), "https://materiall.jp/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatalf("error: %v", r.Err)
		case scraper.KindScene:
			n++
			sc := r.Scene
			if sc.ID != "mtafb-007" || sc.Title != "‘Title’ one" {
				t.Errorf("ID/Title = %q %q", sc.ID, sc.Title)
			}
			if len(sc.Tags) != 2 || sc.Tags[0] != "巨乳" || len(sc.Performers) != 2 || sc.Duration != 1291*60 {
				t.Errorf("scene = %+v", sc)
			}
			if !sc.Date.Equal(time.Date(2026, 3, 26, 0, 0, 0, 0, time.UTC)) || sc.Thumbnail == "" {
				t.Errorf("Date/Thumb = %v %q", sc.Date, sc.Thumbnail)
			}
		}
	}
	if n != 1 {
		t.Fatalf("scenes = %d", n)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("https://materiall.jp/") || !s.MatchesURL("https://materiall.jp/works/x/") || s.MatchesURL("https://example.com/") {
		t.Error("MatchesURL")
	}
}
