package buddhabang

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://buddhabangxxx.com/",
		"https://www.buddhabangxxx.com/tour/index.php",
		"https://buddhabangxxx.com/video/",
		"https://buddhabangxxx.com/video-category/kally-xo/",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{
		"https://buddhabangxxx.com.evil.com/",
		"https://www.baeegyptxxx.com/tour/index.php",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestListingPath(t *testing.T) {
	cases := map[string]struct {
		path  string
		skips bool
	}{
		"https://www.buddhabangxxx.com/tour/index.php":            {"/video/", true},
		"https://buddhabangxxx.com/video/":                        {"/video/", true},
		"https://buddhabangxxx.com/video-category/kally-xo/":      {"/video-category/kally-xo/", false},
		"https://buddhabangxxx.com/video-category/scenes/page/3/": {"/video-category/scenes/", false},
		"https://buddhabangxxx.com/video-category/classic-scenes": {"/video-category/classic-scenes/", false},
	}
	for in, want := range cases {
		p, skip := listingPath(in)
		if p != want.path || skip != want.skips {
			t.Errorf("listingPath(%q) = %q,%v; want %q,%v", in, p, skip, want.path, want.skips)
		}
	}
}

func TestParseListing(t *testing.T) {
	cards, total := parseListing(fixture(t, "listing_page1.html"))
	if total != 2 {
		t.Errorf("total pages = %d, want 2 (sidebar's 90 must be ignored)", total)
	}
	if len(cards) != 3 {
		t.Fatalf("got %d cards, want 3 (sidebar card excluded)", len(cards))
	}
	c := cards[0]
	if c.id != "20996" || c.title != "A WITCHES QUEST FOR BBC" ||
		c.url != "https://buddhabangxxx.com/video/a-witches-quest-for-bbc/" {
		t.Errorf("card 0 = %+v", c)
	}
	if c.thumb != "https://buddhabangxxx.com/wp-content/uploads/1-1600x900.jpg" {
		t.Errorf("thumb = %q, want the widest srcset entry", c.thumb)
	}
	if !onlyShorts(cards[2].categories) || onlyShorts(cards[0].categories) {
		t.Errorf("shorts detection wrong: %v / %v", cards[2].categories, cards[0].categories)
	}
}

func TestParseFeed(t *testing.T) {
	items, err := parseFeed(fixture(t, "feed_page1.xml"))
	if err != nil {
		t.Fatal(err)
	}
	it, ok := items["/video/a-witches-quest-for-bbc"]
	if !ok {
		t.Fatalf("post 20996 missing from %v", items)
	}
	if want := time.Date(2026, 10, 28, 4, 40, 40, 0, time.UTC); !it.date.Equal(want) {
		t.Errorf("date = %v, want %v", it.date, want)
	}
	if it.description == "" || it.description[:len("BuddhaBangxxx.com presents,")] != "BuddhaBangxxx.com presents," {
		t.Errorf("description = %q", it.description)
	}
}

func newTestServer(t *testing.T, hits map[string]int, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		mu.Lock()
		hits[key]++
		mu.Unlock()
		switch key {
		case "/video/":
			_, _ = w.Write(fixture(t, "listing_page1.html"))
		case "/video/page/2/":
			_, _ = w.Write(fixture(t, "listing_page2.html"))
		case "/video/feed/":
			_, _ = w.Write(fixture(t, "feed_page1.xml"))
		case "/video/feed/?paged=2":
			_, _ = w.Write(fixture(t, "feed_page2.xml"))
		default:
			http.NotFound(w, r)
		}
	}))
}

func collect(t *testing.T, ch <-chan scraper.SceneResult) ([]models.Scene, bool) {
	t.Helper()
	var scenes []models.Scene
	stopped := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Errorf("scrape error: %v", r.Err)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return scenes, stopped
}

func TestListScenes(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	srv := newTestServer(t, hits, &mu)
	defer srv.Close()

	s := &Scraper{client: srv.Client(), base: srv.URL}
	studioURL := "https://www.buddhabangxxx.com/tour/index.php"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _ := collect(t, ch)
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3 (short skipped)", len(scenes))
	}
	for _, sc := range scenes {
		if sc.StudioURL != studioURL || sc.SiteID != siteID || sc.Studio != studioName {
			t.Errorf("scene %s not stamped: %+v", sc.ID, sc)
		}
		if sc.Date.IsZero() || sc.Description == "" {
			t.Errorf("scene %s missing feed data: date=%v desc=%q", sc.ID, sc.Date, sc.Description)
		}
	}
	last := scenes[2]
	if last.ID != "6130" || last.Date.Year() != 2021 || last.Description != "Alexis Love takes on Stretch & King Nasir." {
		t.Errorf("last scene = %+v", last)
	}
	if hits["/video/page/3/"] != 0 {
		t.Errorf("walked past data-total-pages")
	}
	if hits["/video/feed/?paged=3"] != 0 {
		t.Errorf("feed read further than needed")
	}
}

func TestListScenesKnownIDsStopsAfterPage(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	srv := newTestServer(t, hits, &mu)
	defer srv.Close()

	s := &Scraper{client: srv.Client(), base: srv.URL}
	ch, _ := s.ListScenes(context.Background(), "https://buddhabangxxx.com/",
		scraper.ListOpts{KnownIDs: map[string]bool{"20996": true}})
	scenes, stopped := collect(t, ch)
	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if len(scenes) != 1 || scenes[0].ID != "20994" {
		t.Errorf("got %v, want only 20994", scenes)
	}
	if hits["/video/page/2/"] != 0 || hits["/video/feed/?paged=2"] != 0 {
		t.Errorf("walked past the page holding a known ID: %v", hits)
	}
}
