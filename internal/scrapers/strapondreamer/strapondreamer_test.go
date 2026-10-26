package strapondreamer

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

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://strapondreamer.com/":           true,
		"https://www.strapondreamer.com/videos": true,
		"https://strapondreamer.com.example":    false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v", u, got)
		}
	}
}

func TestParseDetail(t *testing.T) {
	sc, err := parseDetail(fixture(t, "detail.html"), "NV8232089", "https://strapondreamer.com/videos/NV8232089/x", "https://strapondreamer.com/", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sc.Title != "The Squirting Strapon Gangbang - 3 Girls Fuck a Guy in the Ass!" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Duration != 40*60 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if len(sc.Tags) != 1 || sc.Tags[0] != "strapon" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if !strings.HasPrefix(sc.Description, "Every strapon lovers dream come true!") || strings.Contains(sc.Description, "&#039;") {
		t.Errorf("Description = %q", sc.Description)
	}
	if sc.Thumbnail != "https://strapondreamer.com/assets/NV8232089/slide_1.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
}

func TestParseDetailWithoutTitle(t *testing.T) {
	if _, err := parseDetail("<html></html>", "x", "u", "s", time.Now()); scraper.Classify(err) != scraper.FailureParse {
		t.Errorf("err = %v", err)
	}
}

func newServer(t *testing.T, failID string) {
	t.Helper()
	listing, detail := fixture(t, "listing.html"), fixture(t, "detail.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/videos" && r.URL.Query().Get("page") == "":
			_, _ = w.Write([]byte(listing))
		case r.URL.Path == "/videos":
			http.NotFound(w, r)
		case failID != "" && strings.HasPrefix(r.URL.Path, "/videos/"+failID+"/"):
			http.NotFound(w, r)
		case strings.HasPrefix(r.URL.Path, "/videos/NV"):
			_, _ = w.Write([]byte(detail))
		default:
			http.NotFound(w, r)
		}
	}))
	old := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = old; srv.Close() })
}

func run(t *testing.T, opts scraper.ListOpts) (ids []string, errs int, stopped bool) {
	t.Helper()
	ch, _ := New().ListScenes(context.Background(), "https://strapondreamer.com/", opts)
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			if r.Scene.StudioURL != "https://strapondreamer.com/" {
				t.Errorf("StudioURL = %q", r.Scene.StudioURL)
			}
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			errs++
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return
}

// Page 2 is linked but answers 404, which ends the walk cleanly.
func TestWalkEndsOn404(t *testing.T) {
	newServer(t, "")
	ids, errs, _ := run(t, scraper.ListOpts{})
	if len(ids) != 3 || errs != 0 {
		t.Errorf("ids=%v errs=%d", ids, errs)
	}
}

func TestKnownIDStopsAfterThePage(t *testing.T) {
	newServer(t, "")
	ids, _, stopped := run(t, scraper.ListOpts{KnownIDs: map[string]bool{"NV8232089": true}})
	if len(ids) != 2 || !stopped {
		t.Errorf("ids=%v stopped=%v", ids, stopped)
	}
}

func TestDetailFailureIsReported(t *testing.T) {
	newServer(t, "NV8232657")
	ids, errs, _ := run(t, scraper.ListOpts{})
	if len(ids) != 2 || errs != 1 {
		t.Errorf("ids=%v errs=%d", ids, errs)
	}
}
