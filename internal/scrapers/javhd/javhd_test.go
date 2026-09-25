package javhd

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

func thumb(id, link, title string) string {
	return fmt.Sprintf(`<thumb-component
          type-thumb="video"
          video-id="%s"
          link-content="%s"
          url-thumb="https://c4.cdnjhd.com/thumbs/%s.jpg"
          video-preview="https://c4.cdnjhd.com/trailers/%s/5sec.mp4"
                        has-label="premium"
                    title="%s"
          views="12"
          likes="50"
          :age-restricted="null"
                ></thumb-component>`, id, link, id, id, title)
}

// A photo set uses the same component with a different type.
const photoThumb = `<thumb-component type-thumb="photo" video-id="999" title="A gallery"></thumb-component>`

func newTestServer(total, perPage int, pages map[int]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, listingPath) {
			http.NotFound(w, r)
			return
		}
		page, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, listingPath))
		tpl, ok := pages[page]
		if !ok {
			// Past the last page the tour answers 404, not an empty list.
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(listingResponse{
			Status: 1, Template: tpl, Results: total, PerPage: perPage,
		})
	}))
}

func newTestScraper(ts *httptest.Server) *Scraper {
	return &Scraper{client: ts.Client(), base: ts.URL}
}

func collect(ch <-chan scraper.SceneResult) (scenes []models.Scene, errs, stopped, total int) {
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs++
		case scraper.KindStoppedEarly:
			stopped++
		case scraper.KindTotal:
			total = r.Total
		}
	}
	return scenes, errs, stopped, total
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://javhd.com/", true},
		{"https://www.javhd.com/en/japanese-porn-videos", true},
		{"http://javhd.com", true},
		{"https://javhd.com.evil.net/", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestParseListing(t *testing.T) {
	tpl := thumb("86816", "https://javhd.com/en/studio/room/1pondo/video/1789-natural-beauty", "Natural beauty &amp; friends") +
		photoThumb +
		thumb("86818", "", "Second")
	cards := parseListing(tpl)
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2 (the photo thumb is not a scene)", len(cards))
	}
	c := cards[0]
	if c.id != "86816" {
		t.Errorf("id = %q", c.id)
	}
	if c.title != "Natural beauty & friends" {
		t.Errorf("title = %q", c.title)
	}
	if c.views != 12 || c.likes != 50 {
		t.Errorf("views/likes = %d/%d", c.views, c.likes)
	}
	if c.preview != "https://c4.cdnjhd.com/trailers/86816/5sec.mp4" {
		t.Errorf("preview = %q", c.preview)
	}
}

func TestSceneURL(t *testing.T) {
	s := New()
	got := s.sceneURL(card{id: "86816", link: "https://javhd.com/en/studio/room/1pondo/video/1789-x"})
	if got != "https://javhd.com/en/studio/room/1pondo/video/1789-x" {
		t.Errorf("sceneURL = %q", got)
	}
	// A card with no link still needs a URL.
	if got := s.sceneURL(card{id: "86816"}); got != "https://javhd.com/en/id/86816" {
		t.Errorf("sceneURL fallback = %q", got)
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer(3, 2, map[int]string{
		1: thumb("1", "https://javhd.com/en/id/1/one", "One") + thumb("2", "", "Two"),
		2: thumb("3", "", "Three"),
	})
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _, total := collect(ch)
	if errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	if !strings.HasPrefix(scenes[0].URL, ts.URL) {
		t.Errorf("URL = %q, want the test server host", scenes[0].URL)
	}
	if scenes[0].Studio != studioName || scenes[0].SiteID != siteID {
		t.Errorf("scene = %+v", scenes[0])
	}
}

// Past the last page the tour answers HTTP 404, so the walk has to stop on the
// counts it was given rather than asking for another page.
func TestWalkStopsOnTheReportedTotal(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, listingPath) {
			http.NotFound(w, r)
			return
		}
		calls++
		page, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, listingPath))
		if page > 1 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(listingResponse{
			Status: 1, Template: thumb("1", "", "One") + thumb("2", "", "Two"), Results: 2, PerPage: 2,
		})
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _, _ := collect(ch)
	if errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	if len(scenes) != 2 {
		t.Errorf("got %d scenes, want 2", len(scenes))
	}
	if calls != 1 {
		t.Errorf("requested %d pages, want 1", calls)
	}
}

// The listing's order is neither date nor id, so a known scene must not stop
// the walk.
func TestKnownIDsDoNotStopTheWalk(t *testing.T) {
	ts := newTestServer(3, 2, map[int]string{
		1: thumb("1", "", "One") + thumb("2", "", "Two"),
		2: thumb("3", "", "Three"),
	})
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{
		KnownIDs: map[string]bool{"2": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, stopped, _ := collect(ch)
	if len(scenes) != 3 {
		t.Errorf("got %d scenes, want all 3", len(scenes))
	}
	if stopped != 0 {
		t.Errorf("got %d stoppedEarly, want 0", stopped)
	}
}

// A first page that answers but renders no thumbs is a parser or site change,
// not an empty catalogue.
func TestEmptyFirstPageIsAParseError(t *testing.T) {
	ts := newTestServer(0, 36, map[int]string{1: `<div class="empty"></div>`})
	defer ts.Close()

	s := newTestScraper(ts)
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
