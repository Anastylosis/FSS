package digitalark

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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

func TestParseListing(t *testing.T) {
	cards, total := parseListing(fixture(t, "list.html"))
	if total != 292 || len(cards) != 3 {
		t.Fatalf("total=%d cards=%d", total, len(cards))
	}
	c := cards[0]
	if c.id != "000000002231" || c.title != "ぶっかけ爆乳デカ尻秘書は乳首ビンビンにして社員を誘惑する変態ドスケベ痴女" {
		t.Errorf("card = %+v", c)
	}
	if !strings.HasSuffix(c.thumb, "/000000002231_B14u0NL.jpg") || c.price != 3190 {
		t.Errorf("thumb/price = %q %v", c.thumb, c.price)
	}
	if cards[1].id != "000000002220" || cards[2].id != "000000002211" {
		t.Errorf("ids = %s %s", cards[1].id, cards[2].id)
	}
}

func TestApplyDetail(t *testing.T) {
	sc := models.Scene{Studio: studioName}
	applyDetail(&sc, fixture(t, "item.html"))
	if sc.Title != "ぶっかけ爆乳デカ尻秘書は乳首ビンビンにして社員を誘惑する変態ドスケベ痴女" {
		t.Errorf("Title = %q", sc.Title)
	}
	if !sc.Date.Equal(time.Date(2025, 12, 23, 0, 0, 0, 0, time.UTC)) || sc.Duration != 160*60 {
		t.Errorf("Date/Duration = %v %d", sc.Date, sc.Duration)
	}
	if sc.Director != "カシマ" || sc.Studio != "WEEKENDER" || sc.Series != "ぶっかけ爆乳デカ尻秘書" {
		t.Errorf("Director/Studio/Series = %q %q %q", sc.Director, sc.Studio, sc.Series)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "九井スナオ" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if strings.Join(sc.Tags, ",") != "OL,パンスト,タイツ,尻,巨乳,痴女" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if !strings.HasPrefix(sc.Description, "服の上からでもぷっくり乳首") || !strings.HasSuffix(sc.Thumbnail, "000000002231_B14u0NL.jpg") {
		t.Errorf("Description = %q, Thumbnail = %q", sc.Description, sc.Thumbnail)
	}
}

func TestApplyDetailWithoutCast(t *testing.T) {
	sc := models.Scene{Studio: studioName}
	applyDetail(&sc, fixture(t, "item_compilation.html"))
	if sc.Title != "JK集団SEX部" || !sc.Date.Equal(time.Date(2017, 9, 7, 0, 0, 0, 0, time.UTC)) || sc.Duration != 120*60 {
		t.Errorf("Title/Date/Duration = %q %v %d", sc.Title, sc.Date, sc.Duration)
	}
	if sc.Performers != nil || sc.Director != "" || sc.Series != "" || sc.Studio != studioName {
		t.Errorf("Performers/Director/Series/Studio = %v %q %q %q", sc.Performers, sc.Director, sc.Series, sc.Studio)
	}
	if strings.Join(sc.Tags, ",") != "総集編,Wフェラ,集団,女子校生" {
		t.Errorf("Tags = %v", sc.Tags)
	}
}

func TestListingPath(t *testing.T) {
	for u, want := range map[string]string{
		"http://www.digital-ark.com":                               "/view/category/all_items",
		"https://shop.digital-ark.com/":                            "/view/category/all_items",
		"https://shop.digital-ark.com/view/category/ct5":           "/view/category/ct5",
		"https://shop.digital-ark.com/view/category/ct5?page=3":    "/view/category/ct5",
		"https://shop.digital-ark.com/view/category/hot-chocolate": "/view/category/hot-chocolate",
	} {
		if got := listingPath(u); got != want {
			t.Errorf("listingPath(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestListScenes(t *testing.T) {
	list, item := fixture(t, "list.html"), fixture(t, "item.html")
	var mu sync.Mutex
	var listings, items []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/view/category/"):
			listings = append(listings, r.URL.RequestURI())
			_, _ = w.Write([]byte(list))
		case r.URL.Path == "/view/item/000000002211":
			items = append(items, r.URL.Path)
			http.NotFound(w, r)
		case strings.HasPrefix(r.URL.Path, "/view/item/"):
			items = append(items, r.URL.Path)
			_, _ = w.Write([]byte(item))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	opts := scraper.ListOpts{KnownIDs: map[string]bool{"000000002220": true}}
	ch, _ := New().ListScenes(context.Background(), "https://shop.digital-ark.com/view/category/ct5", opts)
	var scenes []models.Scene
	errs, stopped := 0, false
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs++
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if len(listings) != 1 || listings[0] != "/view/category/ct5?page=1" {
		t.Errorf("listings = %v", listings)
	}
	if len(items) != 2 {
		t.Errorf("detail fetches = %v, want the two unknown items", items)
	}
	if len(scenes) != 2 || errs != 1 || !stopped {
		t.Fatalf("scenes=%d errs=%d stopped=%v", len(scenes), errs, stopped)
	}
	sc := scenes[0]
	if sc.ID != "000000002231" || sc.Studio != "WEEKENDER" || sc.Performers == nil || sc.StudioURL != "https://shop.digital-ark.com/view/category/ct5" {
		t.Errorf("scene = %+v", sc)
	}
	if !strings.HasPrefix(sc.URL, srv.URL) {
		t.Errorf("URL = %q, not the test server", sc.URL)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 3190 || sc.PriceHistory[0].Currency != "JPY" {
		t.Errorf("PriceHistory = %+v", sc.PriceHistory)
	}
	if fallback := scenes[1]; fallback.ID != "000000002211" || fallback.Title == "" || fallback.Studio != studioName {
		t.Errorf("failed detail should keep the card: %+v", fallback)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"http://www.digital-ark.com":                          true,
		"https://digital-ark.com/":                            true,
		"https://shop.digital-ark.com/view/category/ct5":      true,
		"https://shop.digital-ark.com/view/item/000000002231": true,
		"https://digital-ark.com.evil.invalid/":               false,
		"https://notdigital-ark.com/":                         false,
	} {
		if s.MatchesURL(u) != want {
			t.Errorf("MatchesURL(%q) != %v", u, want)
		}
	}
}
