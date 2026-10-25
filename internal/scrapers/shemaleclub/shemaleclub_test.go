package shemaleclub

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

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

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://shemale-club.com/",
		"https://www.shemale-club.com/tour/category.php?id=5",
		"https://shemale-club.com/tour/sets.php?id=155",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	if s.MatchesURL("https://www.tgirl-network.com/") {
		t.Error("matched the network tour")
	}
}

func TestParseCards(t *testing.T) {
	cards, items := parseCards(fixture(t, "listing.html"))
	if items != 2 || len(cards) != 2 {
		t.Fatalf("items=%d cards=%d, want 2/2", items, len(cards))
	}
	c := cards[0]
	if c.id != "3245" || c.title != "Hiilda & Tayssa Halloween" || c.duration != 17*60 {
		t.Errorf("card 0 = %+v", c)
	}
	if c.thumb != "content//contentthumbs/26027.jpg" {
		t.Errorf("thumb = %q", c.thumb)
	}
}

func TestParseCardsSkipsPhotoSets(t *testing.T) {
	cards, items := parseCards(fixture(t, "model.html"))
	if items != 2 {
		t.Errorf("items = %d, want 2", items)
	}
	if len(cards) != 1 || cards[0].id != "3245" {
		t.Errorf("cards = %+v, want only the movie", cards)
	}
}

func TestParseDetail(t *testing.T) {
	d := parseDetail(fixture(t, "detail.html"))
	if d.title != "Hiilda & Tayssa Halloween" {
		t.Errorf("title = %q", d.title)
	}
	if len(d.performers) != 1 || d.performers[0] != "Hilda Brazil" {
		t.Errorf("performers = %v", d.performers)
	}
	if strings.Join(d.tags, "|") != "Hardcore|lost and found|TGirl Girl|Tgirl Tgirl|Throwback Thursday" {
		t.Errorf("tags = %v", d.tags)
	}
	if d.duration != 17*60 {
		t.Errorf("duration = %d", d.duration)
	}
}

func TestHasPageLink(t *testing.T) {
	body := `<a href="category.php?id=5&page=2">2</a><a href="category.php?id=5&page=59">59</a>`
	if !hasPageLink(body, 2) || hasPageLink(body, 3) || hasPageLink(body, 5) {
		t.Error("hasPageLink misread the pager")
	}
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, []error) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	var errs []error
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	return scenes, errs
}

func TestScrapeWalksPagerAndStops(t *testing.T) {
	listing := fixture(t, "listing.html")
	detail := fixture(t, "detail.html")
	var pages atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tour/category.php":
			pages.Add(1)
			if r.URL.Query().Get("id") != "5" {
				t.Errorf("category = %q", r.URL.Query().Get("id"))
			}
			switch r.URL.Query().Get("page") {
			case "":
				_, _ = fmt.Fprint(w, listing) // links page=2
			case "2":
				// The last page: same template, ids moved on, no link to page 3.
				_, _ = fmt.Fprint(w, strings.NewReplacer("id=3245", "id=1001", "id=3243", "id=1000", "page=", "pg=").Replace(listing))
			default:
				t.Errorf("fetched past the last page: %q", r.URL.RawQuery)
			}
		case "/tour/index.php":
			_, _ = fmt.Fprint(w, detail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.base = srv.URL
	const studioURL = "https://shemale-club.com/"
	scenes, errs := collect(t, s, studioURL, scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if pages.Load() != 2 || len(scenes) != 4 {
		t.Fatalf("pages=%d scenes=%d, want 2/4", pages.Load(), len(scenes))
	}
	sc := scenes[0]
	if sc.StudioURL != studioURL || sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("identity = %q %q %q", sc.StudioURL, sc.SiteID, sc.Studio)
	}
	if sc.URL != srv.URL+"/tour/index.php?id=3245" {
		t.Errorf("url = %q", sc.URL)
	}
	if sc.Thumbnail != srv.URL+"/tour/content/contentthumbs/26027.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if len(sc.Performers) != 1 || len(sc.Tags) == 0 || sc.Duration != 1020 {
		t.Errorf("detail not applied: %+v", sc)
	}
}

func TestModelURLScrapesSetsPage(t *testing.T) {
	model := fixture(t, "model.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tour/sets.php":
			if r.URL.Query().Get("id") != "155" || r.URL.Query().Get("page") != "" {
				t.Errorf("model query = %q", r.URL.RawQuery)
			}
			_, _ = fmt.Fprint(w, model)
		case "/tour/index.php":
			_, _ = fmt.Fprint(w, `<h3> Title<span></span></h3>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := New()
	s.Client = srv.Client()
	s.base = srv.URL
	scenes, errs := collect(t, s, "https://shemale-club.com/tour/sets.php?id=155", scraper.ListOpts{})
	if len(errs) != 0 || len(scenes) != 1 {
		t.Fatalf("scenes=%d errs=%v", len(scenes), errs)
	}
}

func TestFirstPageWithoutCardsIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<html>redesigned</html>")
	}))
	defer srv.Close()
	s := New()
	s.Client = srv.Client()
	s.base = srv.URL
	_, errs := collect(t, s, "https://shemale-club.com/", scraper.ListOpts{})
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Fatalf("errs = %v", errs)
	}
}
