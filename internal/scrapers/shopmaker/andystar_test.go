package shopmaker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestAndyStarMatchesURL(t *testing.T) {
	s := newFor("andystar")
	for _, u := range []string{
		"https://www.andy-star-videos.com/",
		"https://andy-star-videos.com/collections?category=Milf",
		"https://www.andy-star-videos.com/models/dirty-priscilla",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://andy-star.com/", "https://kinkymistresses.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
	if s.base != "https://www.andy-star-videos.com" {
		t.Errorf("base = %q", s.base)
	}
}

func TestParseTitleCards(t *testing.T) {
	items := parseListing(readFixture(t, "andystar_listing.html"))
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	if first.id != "1062035302" {
		t.Errorf("id = %q", first.id)
	}
	if first.path != "/collections/milf-in-lost-place-blowjob-doggy-creampie-until-she-overflows" {
		t.Errorf("path = %q", first.path)
	}
	if first.title != "MILF in Lost Place - Blowjob, Doggy & Creampie until she overflows" {
		t.Errorf("title = %q", first.title)
	}
	if first.duration != 622 {
		t.Errorf("duration = %d, want 622", first.duration)
	}
	if !strings.HasPrefix(first.thumbnail, "https://images5.shopmaker.com/") {
		t.Errorf("thumbnail = %q", first.thumbnail)
	}
	if !strings.Contains(first.preview, ".mp4") {
		t.Errorf("preview = %q", first.preview)
	}
	if first.date != "" || len(first.performers) != 0 {
		t.Errorf("the card carries no date or models, got %q %v", first.date, first.performers)
	}
	if items[1].id == first.id || items[1].title == "" {
		t.Errorf("second card = %+v", items[1])
	}
}

func TestDetailMetaFillsWhatTheTitleCardLacks(t *testing.T) {
	var item listItem
	applyMeta(detailMeta(readFixture(t, "andystar_detail.html")), &item)
	if item.date != "2026-10-16" {
		t.Errorf("date = %q", item.date)
	}
	if len(item.performers) != 1 || item.performers[0] != "Dirty-Priscilla" {
		t.Errorf("performers = %v", item.performers)
	}
	if strings.Join(item.categories, ",") != "Milf,Outdoors,POV" {
		t.Errorf("categories = %v", item.categories)
	}
	if d := parseDescription(readFixture(t, "andystar_detail.html")); !strings.HasPrefix(d, "I walk through a Lost Place") {
		t.Errorf("description = %q", d)
	}
}

func TestParseModelPageCards(t *testing.T) {
	items := parseListing(readFixture(t, "andystar_model.html"))
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	if items[0].id != "1062035302" || items[0].date != "2026-10-16" || items[0].duration != 622 {
		t.Errorf("first = %+v", items[0])
	}
	if len(items[0].performers) != 1 || items[0].performers[0] != "Dirty-Priscilla" {
		t.Errorf("performers = %v", items[0].performers)
	}
	if got := withDuration([]listItem{{id: "a"}, {id: "b", duration: 5}}); len(got) != 1 || got[0].id != "b" {
		t.Errorf("withDuration = %+v", got)
	}
}

func TestParseListingURLModes(t *testing.T) {
	cases := []struct {
		url, kind, page1, page3 string
		videoOnly               bool
	}{
		{"https://www.andy-star-videos.com/", "full catalogue", "/collections/page/1?media=video", "/collections/page/3?media=video", false},
		{"https://www.andy-star-videos.com/collections?category=Milf", "category Milf", "/collections/page/1?category=Milf&media=video", "/collections/page/3?category=Milf&media=video", false},
		{"https://www.andy-star-videos.com/collections?media=video&category=Anal+Sex", "category Anal+Sex", "/collections/page/1?category=Anal+Sex&media=video", "/collections/page/3?category=Anal+Sex&media=video", false},
		{"https://www.andy-star-videos.com/models/dirty-priscilla", "model dirty-priscilla", "/models/dirty-priscilla", "/models/dirty-priscilla/page/3", true},
		{"https://www.andy-star-videos.com/models/dirty-priscilla/page/2#x", "model dirty-priscilla", "/models/dirty-priscilla", "/models/dirty-priscilla/page/3", true},
	}
	for _, c := range cases {
		l := parseListingURL(c.url)
		if l.kind != c.kind || l.pagePath(1) != c.page1 || l.pagePath(3) != c.page3 || l.videoOnly != c.videoOnly {
			t.Errorf("%s -> %s %s %s %v", c.url, l.kind, l.pagePath(1), l.pagePath(3), l.videoOnly)
		}
	}
}

type recorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *recorder) add(u string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, u)
}

func runAndyStar(t *testing.T, studioURL string, handler func(path string) []byte) ([]models.Scene, []string) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.URL.RequestURI())
		if b := handler(r.URL.Path); b != nil {
			_, _ = w.Write(b)
			return
		}
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer srv.Close()

	s := newFor("andystar")
	s.base = srv.URL
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindError:
			t.Errorf("error result: %v", res.Err)
		}
	}
	return scenes, rec.seen
}

func TestAndyStarListScenesFullCatalogue(t *testing.T) {
	const studioURL = "https://www.andy-star-videos.com/"
	scenes, seen := runAndyStar(t, studioURL, func(p string) []byte {
		switch {
		case p == "/collections/page/1":
			return readFixture(t, "andystar_listing.html")
		case strings.HasPrefix(p, "/collections/") && !strings.Contains(p, "/page/"):
			return readFixture(t, "andystar_detail.html")
		}
		return nil
	})
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	byID := map[string]models.Scene{}
	for _, sc := range scenes {
		byID[sc.ID] = sc
	}
	sc := byID["1062035302"]
	if sc.Title != "MILF in Lost Place - Blowjob, Doggy & Creampie until she overflows" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.Date.Format("2006-01-02") != "2026-10-16" {
		t.Errorf("date = %v", sc.Date)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Dirty-Priscilla" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if sc.Duration != 622 || sc.Studio != "Andy-Star" || sc.StudioURL != studioURL || sc.SiteID != "andystar" {
		t.Errorf("scene = %+v", sc)
	}
	if !strings.Contains(sc.URL, "/collections/milf-in-lost-place") || strings.Contains(sc.URL, "locale") {
		t.Errorf("URL = %q", sc.URL)
	}
	if !strings.HasPrefix(sc.Description, "I walk through a Lost Place") {
		t.Errorf("description = %q", sc.Description)
	}
	for _, u := range seen {
		if !strings.Contains(u, "locale=en") {
			t.Errorf("request %q does not pin the locale", u)
		}
	}
}

func TestAndyStarModelAndCategoryModes(t *testing.T) {
	scenes, seen := runAndyStar(t, "https://www.andy-star-videos.com/models/dirty-priscilla", func(p string) []byte {
		switch {
		case p == "/models/dirty-priscilla":
			return readFixture(t, "andystar_model.html")
		case strings.HasPrefix(p, "/collections/"):
			return readFixture(t, "andystar_detail.html")
		}
		return nil
	})
	if len(scenes) != 2 {
		t.Fatalf("model mode: got %d scenes, want 2", len(scenes))
	}
	if scenes[0].Date.IsZero() || len(scenes[0].Performers) != 1 {
		t.Errorf("model card fields lost: %+v", scenes[0])
	}
	if !contains(seen, "/models/dirty-priscilla/page/2?locale=en") {
		t.Errorf("model pager not walked: %v", seen)
	}

	_, seen = runAndyStar(t, "https://www.andy-star-videos.com/collections?category=Milf", func(p string) []byte {
		if p == "/collections/page/1" {
			return readFixture(t, "andystar_listing.html")
		}
		if strings.HasPrefix(p, "/collections/") && !strings.Contains(p, "/page/") {
			return readFixture(t, "andystar_detail.html")
		}
		return nil
	})
	if !contains(seen, "/collections/page/1?category=Milf&media=video&locale=en") {
		t.Errorf("category listing not requested: %v", seen)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
