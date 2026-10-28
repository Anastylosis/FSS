package guybone

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
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

func TestApplyDetail(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, fixture(t, "video.html"))
	if sc.Title != "Adam Tops Robin Raw" || !sc.Date.Equal(time.Date(2024, 2, 14, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Title/Date = %q %v", sc.Title, sc.Date)
	}
	if len(sc.Performers) != 2 || sc.Performers[0] != "Adam Surge" || sc.Performers[1] != "Robin Pierce" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if !strings.HasPrefix(sc.Description, "Off the bat, Adam and Robin") || !strings.HasSuffix(sc.Thumbnail, "gba0267.jpg") {
		t.Errorf("Description = %q, Thumbnail = %q", sc.Description, sc.Thumbnail)
	}
}

func TestRunPagesThroughThePostForm(t *testing.T) {
	list, video := fixture(t, "list.html"), fixture(t, "video.html")
	var starts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/list_videos" && r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			starts = append(starts, string(b))
			if string(b) == "start=0" {
				_, _ = w.Write([]byte(list))
				return
			}
			_, _ = w.Write([]byte("<html></html>"))
		case strings.HasPrefix(r.URL.Path, "/video/cumpilation_26"):
			http.Error(w, "x", http.StatusInternalServerError)
		case strings.HasPrefix(r.URL.Path, "/video/"):
			_, _ = w.Write([]byte(video))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, _ := New().ListScenes(context.Background(), "https://guybone.com/", scraper.ListOpts{})
	scenes, errs := 0, 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.StudioURL != "https://guybone.com/" {
				t.Errorf("StudioURL = %q", r.Scene.StudioURL)
			}
		case scraper.KindError:
			errs++
		}
	}
	if scenes != 3 || errs != 1 {
		t.Errorf("scenes=%d errs=%d", scenes, errs)
	}
	if len(starts) != 2 || starts[1] != "start=36" {
		t.Errorf("POST bodies = %v", starts)
	}
}
