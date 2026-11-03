package loracross

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://loracrossmodel.com", "https://www.loracrossmodel.com/content/x/"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	if s.MatchesURL("https://notloracrossmodel.com/") {
		t.Error("matched a foreign host")
	}
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/wp/v2/product":
			if r.URL.Query().Get("product_cat") != "21" {
				t.Errorf("product_cat = %q", r.URL.Query().Get("product_cat"))
			}
			if r.URL.Query().Get("page") != "1" {
				http.Error(w, `{"code":"rest_post_invalid_page_number"}`, http.StatusBadRequest)
				return
			}
			w.Header().Set("X-WP-Total", "2")
			w.Header().Set("X-WP-TotalPages", "1")
			_, _ = w.Write(fixture(t, "products.json"))
		case "/wp-json/wp/v2/model":
			_, _ = w.Write(fixture(t, "models.json"))
		case "/wp-json/wp/v2/genre":
			_, _ = w.Write(fixture(t, "genres.json"))
		case "/wp-json/wp/v2/media":
			_, _ = w.Write(fixture(t, "media.json"))
		case "/wp-json/wc/store/products":
			_, _ = w.Write(fixture(t, "store.json"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListScenes(t *testing.T) {
	srv := newServer(t)
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://loracrossmodel.com", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "6525" || sc.Title != "Lora Cross Gets Pussy Ate While Bench Pressing" {
		t.Errorf("scene = %+v", sc)
	}
	if got := strings.Join(sc.Performers, ","); got != "Lora Cross,Sam Stout" {
		t.Errorf("performers = %q", got)
	}
	if len(sc.Tags) != 5 || sc.Tags[0] != "Abs" {
		t.Errorf("tags = %v", sc.Tags)
	}
	if sc.Date.IsZero() || !strings.HasPrefix(sc.Description, "On a naked gym date") {
		t.Errorf("date/description = %v / %q", sc.Date, sc.Description)
	}
	if !strings.HasSuffix(sc.Thumbnail, "foot-fetish-cumshot-pic.png") {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 8 {
		t.Errorf("price = %+v", sc.PriceHistory)
	}
	if scenes[1].Thumbnail != "" {
		t.Errorf("second thumbnail = %q", scenes[1].Thumbnail)
	}
	if scenes[1].PriceHistory[0].Regular != 15 {
		t.Errorf("second price = %+v", scenes[1].PriceHistory)
	}
}

func TestAddPriceSale(t *testing.T) {
	var sc models.Scene
	var sp storeProduct
	sp.Prices.Price, sp.Prices.RegularPrice, sp.Prices.MinorUnit = "500", "800", 2
	addPrice(&sc, sp, sc.ScrapedAt)
	if len(sc.PriceHistory) != 1 || !sc.PriceHistory[0].IsOnSale || sc.PriceHistory[0].Discounted != 5 {
		t.Errorf("price = %+v", sc.PriceHistory)
	}
}
