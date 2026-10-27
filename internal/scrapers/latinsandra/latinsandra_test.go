package latinsandra

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestParseVideos(t *testing.T) {
	b, err := os.ReadFile("testdata/videos.html")
	if err != nil {
		t.Fatal(err)
	}
	scenes := parseVideos(string(b), "s", "https://www.latinsandra.com/user/view_more_video", time.Now())
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes", len(scenes))
	}
	sc := scenes[0]
	if sc.Title != "Huge White Cock Wrecks My Latina Pussy" || sc.ID != "huge-white-cock-wrecks-my-latina-pussy" {
		t.Errorf("Title/ID = %q %q", sc.Title, sc.ID)
	}
	if sc.Preview != "https://www.latinsandra.com/public/videos/Huge%20White%20Cock%20Wrecks%20My%20Latina%20Pussy%20LS.mp4" {
		t.Errorf("Preview = %q", sc.Preview)
	}
	if sc.StudioURL != "s" || len(sc.Performers) != 1 {
		t.Errorf("scene = %+v", sc)
	}
}

func TestEmptyPageIsAParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>maintenance</html>"))
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()
	ch, _ := New().ListScenes(context.Background(), "s", scraper.ListOpts{})
	errs := 0
	for r := range ch {
		if r.Kind == scraper.KindError && scraper.Classify(r.Err) == scraper.FailureParse {
			errs++
		}
	}
	if errs != 1 {
		t.Errorf("parse errors = %d", errs)
	}
}
