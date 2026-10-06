package kvssitemaputil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const liveHost = "https://barbisinclair.com"

func testConfig(base string) SiteConfig {
	return SiteConfig{
		SiteID:      "barbisinclair",
		Base:        base,
		StudioName:  "Barbi Sinclair",
		Filters:     []string{"models", "tags"},
		PerformerRe: regexp.MustCompile(`(?s)<h3 class="media-box__title">(.*?)</h3>`),
		TagRe:       regexp.MustCompile(`(?s)<a class="btn" href="[^"]*/tags/[^"]*">(.*?)</a>`),
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newScraper(t *testing.T) (*Scraper, string) {
	t.Helper()
	listing := fixture(t, "listing.html")
	detail := fixture(t, "detail.html")
	srv := testutil.SitemapServerFunc(t, liveHost, fixture(t, "sitemap.xml"), func(path string) []byte {
		switch {
		case strings.HasPrefix(path, "/models/") || strings.HasPrefix(path, "/tags/"):
			if strings.HasSuffix(path, "/2/") {
				return []byte("<html><body><div id=\"list_videos_common_videos_content_list\"></div></body></html>")
			}
			return listing
		case strings.HasPrefix(path, "/videos/1303/"):
			return []byte(strings.ReplaceAll(string(detail), "Lucky Guy", "Zophia"))
		default:
			return detail
		}
	})
	s := New(testConfig(srv.URL))
	s.Client = srv.Client()
	return s, srv.URL
}

func collect(t *testing.T, s *Scraper, url string, opts scraper.ListOpts) ([]models.Scene, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := s.ListScenes(ctx, url, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	total := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Fatalf("unexpected error: %v", r.Err)
		case scraper.KindTotal:
			total = r.Total
		}
	}
	return scenes, total
}

func TestListScenes(t *testing.T) {
	s, base := newScraper(t)
	scenes, total := collect(t, s, base, scraper.ListOpts{})
	if total != 2 || len(scenes) != 2 {
		t.Fatalf("total=%d scenes=%d, want 2/2", total, len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "1303" || sc.Title != "Zophia Works Overtime" {
		t.Errorf("first scene = %q %q", sc.ID, sc.Title)
	}
	if sc.Date.Format("2006-01-02") != "2021-09-25" || sc.Duration != 1245 {
		t.Errorf("date=%v duration=%d", sc.Date, sc.Duration)
	}
	if !strings.HasPrefix(sc.URL, base+"/videos/1303/") || sc.StudioURL != base || sc.Studio != "Barbi Sinclair" {
		t.Errorf("url=%q studioURL=%q studio=%q", sc.URL, sc.StudioURL, sc.Studio)
	}
	if !strings.Contains(sc.Description, "Zophia loves to work overtime") || sc.Thumbnail == "" {
		t.Errorf("description=%q thumb=%q", sc.Description, sc.Thumbnail)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Zophia" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if got := strings.Join(sc.Tags, ","); got != "callyourhusband,amateur" {
		t.Errorf("tags = %q", got)
	}
	if scenes[1].ID != "1264" || scenes[1].Performers[0] != "Lucky Guy" {
		t.Errorf("second scene = %+v", scenes[1])
	}
}

func TestListScenesFilter(t *testing.T) {
	s, base := newScraper(t)
	scenes, total := collect(t, s, base+"/tags/amateur/", scraper.ListOpts{})
	if total != 1 || len(scenes) != 1 || scenes[0].ID != "1264" {
		t.Fatalf("total=%d scenes=%v", total, scenes)
	}
	if scenes[0].StudioURL != base+"/tags/amateur/" {
		t.Errorf("StudioURL = %q", scenes[0].StudioURL)
	}
}

func TestListScenesSkipsKnown(t *testing.T) {
	s, base := newScraper(t)
	scenes, _ := collect(t, s, base, scraper.ListOpts{KnownIDs: map[string]bool{"1303": true}})
	if len(scenes) != 1 || scenes[0].ID != "1264" {
		t.Fatalf("scenes = %v", scenes)
	}
}

func TestSkipPerformers(t *testing.T) {
	cfg := testConfig("https://barbisinclair.com")
	cfg.SkipPerformers = []string{"Lucky Guy"}
	s := New(cfg)
	sc := models.Scene{}
	s.applyDetail(&sc, string(fixture(t, "detail.html")))
	if len(sc.Performers) != 0 || len(sc.Tags) != 1 {
		t.Errorf("performers=%v tags=%v", sc.Performers, sc.Tags)
	}
}

func TestRegion(t *testing.T) {
	cfg := testConfig("https://www.lafranceapoil.com")
	cfg.Region = regexp.MustCompile(`(?s)<div class="buttons-row">(.*?)</div>`)
	cfg.PerformerRe = regexp.MustCompile(`(?s)<a[^>]*href="[^"]*/models/[^"/]+/"[^>]*>(.*?)</a>`)
	cfg.TagRe = nil
	s := New(cfg)
	page := `<a href="https://x/models/">Nav</a><div class="buttons-row"><a href="https://x/models/phoebe/"><i></i> Phoebe </a></div><a href="https://x/models/other/">Other</a>`
	sc := models.Scene{}
	s.applyDetail(&sc, page)
	if len(sc.Performers) != 1 || sc.Performers[0] != "Phoebe" {
		t.Errorf("performers = %v", sc.Performers)
	}
}

func TestListingIDs(t *testing.T) {
	ids := listingIDs(string(fixture(t, "listing.html")))
	if got := strings.Join(ids, ","); got != "591,1264,1279" {
		t.Errorf("ids = %q (sidebar link must be excluded)", got)
	}
	if listingIDs("<html></html>") != nil {
		t.Error("page without a video list must yield nothing")
	}
}

func TestFilter(t *testing.T) {
	cfg := testConfig("https://www.lafranceapoil.com")
	cfg.Filters = []string{"models", "categories", "series"}
	s := New(cfg)
	cases := []struct {
		url, section, slug string
		ok                 bool
	}{
		{"https://www.lafranceapoil.com/models/phoebe/", "models", "phoebe", true},
		{"https://www.lafranceapoil.com/en/categories/amatrice/", "categories", "amatrice", true},
		{"https://www.lafranceapoil.com/series/les-lecons/2/", "series", "les-lecons", true},
		{"https://www.lafranceapoil.com/", "", "", false},
		{"https://www.lafranceapoil.com/videos/", "", "", false},
		{"https://www.lafranceapoil.com/models/", "", "", false},
	}
	for _, c := range cases {
		sec, slug, ok := s.filter(c.url)
		if sec != c.section || slug != c.slug || ok != c.ok {
			t.Errorf("filter(%q) = %q %q %v", c.url, sec, slug, ok)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(testConfig("https://barbisinclair.com"))
	for _, u := range []string{
		"https://barbisinclair.com",
		"https://www.barbisinclair.com/videos/",
		"https://barbisinclair.com/models/alexa-jaymes/",
		"http://barbisinclair.com/tags/amateur/",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	for _, u := range []string{
		"https://barbisinclair.com.evil.net/",
		"https://notbarbisinclair.com/",
		"https://example.com/barbisinclair.com",
	} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %s", u)
		}
	}
	want := []string{"barbisinclair.com", "barbisinclair.com/models/{slug}/", "barbisinclair.com/tags/{slug}/"}
	if got := strings.Join(s.Patterns(), " "); got != strings.Join(want, " ") {
		t.Errorf("patterns = %v", s.Patterns())
	}
}

func TestParseSitemapEmpty(t *testing.T) {
	s := New(testConfig("https://barbisinclair.com"))
	if _, err := s.parseSitemap([]byte(`<urlset></urlset>`), "x", time.Now()); err == nil {
		t.Error("empty sitemap must be an error")
	}
}

func TestApplyDetailLaFranceAPoil(t *testing.T) {
	s := New(SiteConfig{
		SiteID:      "lafranceapoil",
		Base:        "https://www.lafranceapoil.com",
		Region:      regexp.MustCompile(`(?s)<div class="block buttons-row[^"]*">(.*?)</div>`),
		PerformerRe: regexp.MustCompile(`(?s)<a[^>]*href="[^"]*/models/[^"/]+/"[^>]*>(.*?)</a>`),
		TagRe:       regexp.MustCompile(`(?s)<a[^>]*href="[^"]*/categories/[^"/]+/"[^>]*>(.*?)</a>`),
	})
	sc := models.Scene{Tags: []string{"Amatrice"}}
	s.applyDetail(&sc, string(fixture(t, "detail_lfap.html")))
	if got := strings.Join(sc.Performers, ","); got != "Phoebe" {
		t.Errorf("performers = %q (nav and related links must be excluded)", got)
	}
	if got := strings.Join(sc.Tags, ","); got != "Amatrice,Fist Anal,Fist Fucking,Gonzo Hard,Grosse" {
		t.Errorf("tags = %q", got)
	}
}

func TestAcceptLanguageOverride(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Accept-Language")
	}))
	defer srv.Close()
	cfg := testConfig(srv.URL)
	cfg.AcceptLanguage = "fr-FR,fr;q=0.9"
	if _, err := New(cfg).fetch(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if got != "fr-FR,fr;q=0.9" {
		t.Errorf("Accept-Language = %q", got)
	}
}
