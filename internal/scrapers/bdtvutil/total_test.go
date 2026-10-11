package bdtvutil

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// Past its last page the site answers 404, so the walk must stop on the
// listing's own total rather than probe one page further.
func TestStopsAtTotalWithoutRequestingPastTheEnd(t *testing.T) {
	listing := strings.Replace(fixture(t, "listing.html"), "<strong>223</strong>", "<strong>3</strong>", 1)
	var mu sync.Mutex
	var pages []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := strings.CutPrefix(r.URL.Path, "/movie-details/"); ok {
			b, _ := os.ReadFile("testdata/detail_" + id + ".html")
			_, _ = w.Write(b)
			return
		}
		mu.Lock()
		pages = append(pages, r.URL.RawQuery)
		mu.Unlock()
		if r.URL.Query().Get("page") != "1" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(listing))
	}))
	defer ts.Close()

	cfg := harmony
	cfg.Base = ts.URL
	s := New(cfg)
	s.Client = ts.Client()
	if n := len(collect(t, s, "https://harmonyvideo.tv/")); n != 2 {
		t.Errorf("got %d scenes, want 2", n)
	}
	if len(pages) != 1 {
		t.Errorf("listing requests = %v", pages)
	}
}
