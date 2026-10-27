package vrproduce

import (
	"context"
	"fmt"
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

func TestParseWork(t *testing.T) {
	sc, err := parseWork(fixture(t, "work.html"), "vrtm-406", "u", "s", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sc.ID != "VRTM-406" || sc.Title != "新・おふくろさん 瀬戸恵子" {
		t.Errorf("ID/Title = %q %q", sc.ID, sc.Title)
	}
	if len(sc.Performers) != 2 || sc.Performers[0] != "瀬戸恵子" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if len(sc.Tags) != 3 || sc.Director != "芳賀栄太郎" || sc.Series != "V&R PRODUCE" {
		t.Errorf("Tags = %v, Director = %q, Series = %q", sc.Tags, sc.Director, sc.Series)
	}
	if !sc.Date.Equal(time.Date(2019, 1, 11, 0, 0, 0, 0, time.UTC)) || sc.Duration != 70*60 {
		t.Errorf("Date = %v, Duration = %d", sc.Date, sc.Duration)
	}
	if sc.LowestPrice != 2980 || !strings.HasSuffix(sc.Thumbnail, "vrtm406_l.jpg") || sc.Description == "" {
		t.Errorf("price/thumb/desc = %v %q %q", sc.LowestPrice, sc.Thumbnail, sc.Description)
	}
}

func TestParseWorkWithoutTitle(t *testing.T) {
	if _, err := parseWork("<html></html>", "x-1", "u", "s", time.Now()); scraper.Classify(err) != scraper.FailureParse {
		t.Errorf("err = %v", err)
	}
}

func TestPageURL(t *testing.T) {
	if got := pageURL("http://x/2019/?post_type=works", 2); got != "http://x/2019/page/2/?post_type=works" {
		t.Errorf("got %q", got)
	}
	if got := pageURL("http://x/genrecat/vr/", 3); got != "http://x/genrecat/vr/page/3/" {
		t.Errorf("got %q", got)
	}
}

func serve(t *testing.T) {
	t.Helper()
	year, work := fixture(t, "year.html"), fixture(t, "work.html")
	thisYear := fmt.Sprintf("/%d/", time.Now().Year())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == thisYear || r.URL.Path == "/genrecat/vr/":
			_, _ = w.Write([]byte(year))
		case strings.HasPrefix(r.URL.Path, "/works/vrvr-072/"):
			http.Error(w, "x", http.StatusInternalServerError)
		case strings.HasPrefix(r.URL.Path, "/works/"):
			_, _ = w.Write([]byte(work))
		default:
			_, _ = w.Write([]byte("<html>no works</html>"))
		}
	}))
	old := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = old; srv.Close() })
}

func run(t *testing.T, studioURL string, opts scraper.ListOpts) (ids []string, errs int, stopped bool) {
	t.Helper()
	ch, _ := New().ListScenes(context.Background(), studioURL, opts)
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			if r.Scene.StudioURL != studioURL {
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

func TestYearWalkReportsFailedWorks(t *testing.T) {
	serve(t)
	ids, errs, _ := run(t, "http://www.vr-produce.co.jp/", scraper.ListOpts{Workers: 1})
	if len(ids) != 2 || errs != 1 {
		t.Errorf("ids=%v errs=%d", ids, errs)
	}
}

func TestGenreModeSkipsKnown(t *testing.T) {
	serve(t)
	ids, _, stopped := run(t, "http://www.vr-produce.co.jp/genrecat/vr/", scraper.ListOpts{KnownIDs: map[string]bool{"VRTM-468": true}})
	if len(ids) != 1 || !stopped {
		t.Errorf("ids=%v stopped=%v", ids, stopped)
	}
}
