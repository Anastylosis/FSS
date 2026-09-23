package peterskingdom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func siteByID(t *testing.T, id string) SiteConfig {
	t.Helper()
	for _, cfg := range sites {
		if cfg.SiteID == id {
			return cfg
		}
	}
	t.Fatalf("no site %q", id)
	return SiteConfig{}
}

func entry(id int, title, dateGMT, slug string, cats ...string) map[string]any {
	terms := make([]map[string]any, 0, len(cats))
	for _, c := range cats {
		terms = append(terms, map[string]any{"name": c, "taxonomy": "video-category"})
	}
	return map[string]any{
		"id":       id,
		"date_gmt": dateGMT,
		"link":     "https://rawwhitemeat.com/videos/" + slug + "/",
		"title":    map[string]string{"rendered": title},
		"content":  map[string]string{"rendered": "<p>A <strong>blurb</strong> &amp; more</p>\n"},
		"_embedded": map[string]any{
			"wp:featuredmedia": []map[string]string{{"source_url": "https://cdn.example.com/" + slug + ".jpg"}},
			"wp:term": []any{
				terms,
				[]map[string]any{{"name": "Ignored", "taxonomy": "post_tag"}},
			},
		},
	}
}

const detailPage = `<html><body><main id="brx-content">
<h1 class="brxe-post-title vid-single-info__title">Big titty slut</h1>
<div class="brxe-post-meta vid-single-info__cast post-meta"><span class="item">Starring: <a href="https://rawwhitemeat.com/performers/hazel-grace/" aria-label="Read more about Hazel Grace">Hazel Grace</a>, <a href="https://rawwhitemeat.com/performers/jay-meyers/" aria-label="Read more about Jay Meyers">Jay Meyers</a></span></div>
<video src="https://rawwhitemeat.com/preview/rwm-hazel-grace-prv.mp4" poster="https://rawwhitemeat.com/p.webp"></video>
</main></body></html>`

func newTestServer(t *testing.T, pages map[int][]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/wp-json/wp/v2/videos":
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page == 0 {
				page = 1
			}
			items, ok := pages[page]
			if !ok {
				// The live API answers 400 past the last page rather than
				// serving an empty list.
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, `{"code":"rest_post_invalid_page_number"}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
		case strings.HasPrefix(r.URL.Path, "/videos/"):
			_, _ = fmt.Fprint(w, detailPage)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newTestScraper(ts *httptest.Server, cfg SiteConfig) *Scraper {
	s := New(cfg)
	s.client = ts.Client()
	s.base = ts.URL
	return s
}

func collect(ch <-chan scraper.SceneResult) (scenes []models.Scene, errs, stopped int) {
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs++
		case scraper.KindStoppedEarly:
			stopped++
		}
	}
	return scenes, errs, stopped
}

func TestRegisteredSitesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, cfg := range sites {
		if seen[cfg.SiteID] {
			t.Errorf("duplicate site id %q", cfg.SiteID)
		}
		seen[cfg.SiteID] = true
		if cfg.Domain == "" || cfg.StudioName == "" {
			t.Errorf("site %q has an empty domain or studio name", cfg.SiteID)
		}
	}
	if len(sites) != 6 {
		t.Errorf("expected the 6 network sites, got %d", len(sites))
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(siteByID(t, "rawwhitemeat"))
	tests := []struct {
		url  string
		want bool
	}{
		{"https://rawwhitemeat.com/", true},
		{"https://www.rawwhitemeat.com/videos/", true},
		{"http://rawwhitemeat.com", true},
		{"https://rawwhitemeat.com.evil.net/", false},
		{"https://peterskingdom.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

// Two sites answer on www and the rest do not; the base has to follow the
// config rather than the bare domain.
func TestHostOverride(t *testing.T) {
	if got := New(siteByID(t, "rawwhitemeat")).base; got != "https://rawwhitemeat.com" {
		t.Errorf("base = %q", got)
	}
	if got := New(siteByID(t, "mypovfam")).base; got != "https://www.mypovfam.com" {
		t.Errorf("base = %q", got)
	}
}

func TestApplyDetail(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, detailPage)
	if len(sc.Performers) != 2 || sc.Performers[0] != "Hazel Grace" || sc.Performers[1] != "Jay Meyers" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if sc.Preview != "https://rawwhitemeat.com/preview/rwm-hazel-grace-prv.mp4" {
		t.Errorf("preview = %q", sc.Preview)
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer(t, map[int][]map[string]any{
		1: {
			entry(3986, "Asian-Columbiana recibe una buena dosis", "2026-09-04T18:18:22", "asian-columbiana", "Anal", "Asian"),
			entry(3900, "Second &amp; Last", "2026-08-21T18:20:03", "second-and-last", "Facial"),
		},
	})
	defer ts.Close()

	s := newTestScraper(ts, siteByID(t, "rawwhitemeat"))
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collect(ch)
	if errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	sc := scenes[0]
	if sc.ID != "3986" || sc.SiteID != "rawwhitemeat" || sc.Studio != "Raw White Meat" {
		t.Errorf("scene = %+v", sc)
	}
	// The API's absolute link is re-homed onto the host being scraped, so the
	// detail fetch stays on the test server.
	if !strings.HasPrefix(sc.URL, ts.URL) {
		t.Errorf("URL = %q, want the test server host", sc.URL)
	}
	if sc.Description != "A blurb & more" {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.Thumbnail != "https://cdn.example.com/asian-columbiana.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if sc.Date.Format("2006-01-02 15:04") != "2026-09-04 18:18" {
		t.Errorf("date = %v", sc.Date)
	}
	// Only the video-category taxonomy is a category; other terms are ignored.
	if len(sc.Categories) != 2 || sc.Categories[0] != "Anal" || sc.Categories[1] != "Asian" {
		t.Errorf("categories = %v", sc.Categories)
	}
	if len(sc.Performers) != 2 {
		t.Errorf("performers = %v", sc.Performers)
	}
	if scenes[1].Title != "Second & Last" {
		t.Errorf("title = %q", scenes[1].Title)
	}
}

// Past the last page the API answers HTTP 400, so the walk must end on a
// short page rather than asking for another one.
func TestShortPageEndsTheWalk(t *testing.T) {
	var pageCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wp-json/wp/v2/videos" {
			pageCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				entry(1, "One", "2026-09-04T18:18:22", "one"),
			})
			return
		}
		_, _ = fmt.Fprint(w, detailPage)
	}))
	defer ts.Close()

	s := newTestScraper(ts, siteByID(t, "rawwhitemeat"))
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, errs, _ := collect(ch); errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	if pageCalls != 1 {
		t.Errorf("requested %d listing pages, want 1", pageCalls)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := newTestServer(t, map[int][]map[string]any{
		1: {
			entry(3986, "First", "2026-09-04T18:18:22", "first"),
			entry(3900, "Second", "2026-08-21T18:20:03", "second"),
		},
	})
	defer ts.Close()

	s := newTestScraper(ts, siteByID(t, "rawwhitemeat"))
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{
		KnownIDs: map[string]bool{"3900": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, stopped := collect(ch)
	if len(scenes) != 1 || scenes[0].ID != "3986" {
		t.Errorf("scenes = %v, want only 3986", scenes)
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
	// The known scene is not enriched, so its detail page is never fetched.
	if len(scenes[0].Performers) != 2 {
		t.Errorf("performers = %v", scenes[0].Performers)
	}
}

// An endpoint that answers but lists nothing is a parser or site change, not
// an empty catalogue.
func TestEmptyFirstPageIsAParseError(t *testing.T) {
	ts := newTestServer(t, map[int][]map[string]any{1: {}})
	defer ts.Close()

	s := newTestScraper(ts, siteByID(t, "rawwhitemeat"))
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
			if got := scraper.Classify(r.Err); got != scraper.FailureParse {
				t.Errorf("Classify = %v, want FailureParse", got)
			}
		}
	}
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

func TestSiteURLRehomesTheAPILink(t *testing.T) {
	s := New(siteByID(t, "pervertedpov"))
	got := s.siteURL("https://pervertedpov.com/videos/some-scene/")
	if got != "https://pervertedpov.com/videos/some-scene/" {
		t.Errorf("siteURL = %q", got)
	}
	s.base = "http://127.0.0.1:1234"
	if got := s.siteURL("https://pervertedpov.com/videos/some-scene/"); got != "http://127.0.0.1:1234/videos/some-scene/" {
		t.Errorf("siteURL = %q", got)
	}
}
