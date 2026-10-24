package transexhot

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

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://transexhot.com.br":                             true,
		"https://transexhot.com.br/categoria/travesti-loiras/":  true,
		"https://www.transexhot.com.br/travestis/anita-evelyn/": true,
		"https://transexhot.com":                                false,
		"https://example.com/transexhot.com.br":                 false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestParseDetail(t *testing.T) {
	u := "https://transexhot.com.br/videos/travesti-brasileira-anita-evelyn-bem-dotada/"
	sc, err := parseDetail(fixture(t, "detail.html"), u, "https://transexhot.com.br", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sc.ID != "travesti-brasileira-anita-evelyn-bem-dotada" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.Title != "Travesti brasileira Anita Evelyn bem dotada" {
		t.Errorf("Title = %q", sc.Title)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Anita Evelyn" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if len(sc.Tags) < 3 || sc.Tags[0] != "Travesti Femininas" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if !strings.HasPrefix(sc.Description, "Travesti brasileira Anita Evelyn") {
		t.Errorf("Description = %q", sc.Description)
	}
	if sc.Thumbnail != "https://transexhot.com.br/assets/uploads/2020/07/82.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	// The fixture is one of the scenes WordPress stamped on migration day.
	if !sc.Date.IsZero() {
		t.Errorf("Date = %v, want zero for the 2020-11-09 migration stamp", sc.Date)
	}
}

func TestParseDetailKeepsRealDate(t *testing.T) {
	body := strings.Replace(fixture(t, "detail.html"), `"datePublished":"2020-11-09T18:30:52+00:00"`, `"datePublished":"2021-06-14T10:00:00+00:00"`, 1)
	sc, err := parseDetail(body, "https://transexhot.com.br/videos/x/", "s", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2021, 6, 14, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
}

func TestParseDetailWithoutTitleIsParseError(t *testing.T) {
	_, err := parseDetail("<html><body>gone</body></html>", "https://transexhot.com.br/videos/x/", "s", time.Now())
	if scraper.Classify(err) != scraper.FailureParse {
		t.Errorf("err = %v, want parse error", err)
	}
}

func newServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, strings.ReplaceAll(body, "https://transexhot.com.br", "http://"+r.Host))
	}))
	restore := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = restore; srv.Close() })
	return srv
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) (scenes []string, errs int, stopped bool) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			if r.Scene.StudioURL != studioURL {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, studioURL)
			}
			scenes = append(scenes, r.Scene.ID)
		case scraper.KindError:
			errs++
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return
}

const sitemap = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>https://transexhot.com.br/videos/</loc></url>
<url><loc>https://transexhot.com.br/videos/one/</loc></url>
<url><loc>https://transexhot.com.br/videos/two/</loc></url>
<url><loc>https://transexhot.com.br/videos/two/</loc></url>
</urlset>`

func TestSitemapWalk(t *testing.T) {
	detail := fixture(t, "detail.html")
	srv := newServer(t, map[string]string{
		"/videos-sitemap.xml": sitemap,
		"/videos/one/":        detail,
		"/videos/two/":        detail,
	})
	scenes, errs, stopped := collect(t, New(), srv.URL, scraper.ListOpts{})
	if len(scenes) != 2 || errs != 0 || stopped {
		t.Errorf("scenes=%v errs=%d stopped=%v", scenes, errs, stopped)
	}
}

func TestKnownIDsSkipNotStop(t *testing.T) {
	detail := fixture(t, "detail.html")
	srv := newServer(t, map[string]string{
		"/videos-sitemap.xml": sitemap,
		"/videos/two/":        detail,
	})
	scenes, errs, stopped := collect(t, New(), srv.URL, scraper.ListOpts{KnownIDs: map[string]bool{"one": true}})
	if len(scenes) != 1 || scenes[0] != "two" || errs != 0 || !stopped {
		t.Errorf("scenes=%v errs=%d stopped=%v", scenes, errs, stopped)
	}
}

func TestDetailFailureIsReported(t *testing.T) {
	srv := newServer(t, map[string]string{
		"/videos-sitemap.xml": sitemap,
		"/videos/one/":        fixture(t, "detail.html"),
	})
	scenes, errs, _ := collect(t, New(), srv.URL, scraper.ListOpts{})
	if len(scenes) != 1 || errs != 1 {
		t.Errorf("scenes=%v errs=%d", scenes, errs)
	}
}

func TestCategoryListingPaginates(t *testing.T) {
	cat := fixture(t, "category.html")
	detail := fixture(t, "detail.html")
	routes := map[string]string{
		"/categoria/travesti-loiras/":        cat,
		"/categoria/travesti-loiras/page/2/": cat, // nothing new: the walk ends
	}
	var slugs []string
	for _, m := range cardRe.FindAllStringSubmatch(listingBlock(cat), -1) {
		slug := videoSlug(m[1])
		if routes["/videos/"+slug+"/"] == "" {
			slugs = append(slugs, slug)
			routes["/videos/"+slug+"/"] = detail
		}
	}
	if len(slugs) != 3 {
		t.Fatalf("fixture has %d cards, want 3", len(slugs))
	}
	srv := newServer(t, routes)
	studioURL := srv.URL + "/categoria/travesti-loiras/"
	scenes, errs, _ := collect(t, New(), studioURL, scraper.ListOpts{})
	if len(scenes) != 3 || errs != 0 {
		t.Errorf("scenes=%v errs=%d", scenes, errs)
	}
}

func TestPerformerListingEndsOn404(t *testing.T) {
	srv := newServer(t, map[string]string{
		"/travestis/anita-evelyn/":                             fixture(t, "category.html"),
		"/videos/primo-pauzudo-fudeu-o-cu-da-travesti-rabuda/": fixture(t, "detail.html"),
	})
	urls, err := New().collectListing(context.Background(), srv.URL+"/travestis/anita-evelyn/", scraper.ListOpts{})
	if err != nil || len(urls) != 3 {
		t.Errorf("urls=%v err=%v", urls, err)
	}
}

func TestListingBlockIgnoresNavigation(t *testing.T) {
	if got := cardRe.FindAllString(listingBlock(`<a href="https://transexhot.com.br/videos/">x</a>`), -1); len(got) != 0 {
		t.Errorf("found %v outside the card grid", got)
	}
}
