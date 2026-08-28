package vrspy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://vrspy.com/", true},
		{"https://www.vrspy.com", true},
		{"http://vrspy.com/star/angel-youngs", true},
		{"https://vrspy.com/video/naked-yoga", true},
		{"https://vrspy.net/", false},
		{"https://notvrspy.com/", false},
		{"https://example.com/vrspy.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestResolution(t *testing.T) {
	cases := []struct{ in, want string }{
		{"P8K", "8K"},
		{"P5K", "5K"},
		{"1080", "1080"},
		{"", ""},
	}
	for _, c := range cases {
		if got := resolution(c.in); got != c.want {
			t.Errorf("resolution(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCleanDescription(t *testing.T) {
	in := "<h4>A Heading</h4>\n\n<p>First para with a <a href=\"/videos/blowjob\">link</a>.</p>\n\n<p>Second &amp; last.</p>"
	got := cleanDescription(in)
	want := "A Heading\n\nFirst para with a link.\n\nSecond & last."
	if got != want {
		t.Errorf("cleanDescription = %q, want %q", got, want)
	}
	if cleanDescription("") != "" {
		t.Error("empty description should stay empty")
	}
}

func TestPriceSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	p := func(f float64) *float64 { return &f }

	// A null price is "not sold separately", not zero — recording it as a
	// zero-price snapshot would make the scene look free.
	if _, ok := priceSnapshot(gqlVideo{}, now); ok {
		t.Error("a null price should record no snapshot")
	}

	snap, ok := priceSnapshot(gqlVideo{Free: true}, now)
	if !ok || !snap.IsFree {
		t.Errorf("free scene = %+v (ok=%v)", snap, ok)
	}

	snap, ok = priceSnapshot(gqlVideo{Price: p(5.99)}, now)
	if !ok || snap.Regular != 5.99 || snap.IsOnSale {
		t.Errorf("plain price = %+v", snap)
	}

	snap, ok = priceSnapshot(gqlVideo{Price: p(5), PreviousPrice: p(10)}, now)
	if !ok || !snap.IsOnSale || snap.Regular != 10 || snap.Discounted != 5 || snap.DiscountPercent != 50 {
		t.Errorf("sale price = %+v", snap)
	}

	// A "previous price" that is not actually higher is not a sale.
	snap, _ = priceSnapshot(gqlVideo{Price: p(10), PreviousPrice: p(10)}, now)
	if snap.IsOnSale {
		t.Errorf("equal prices should not be a sale: %+v", snap)
	}
}

func TestToScene(t *testing.T) {
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	v := gqlVideo{
		ID:            "42093363-bdf9-45d9-877d-54fae9761db9",
		Name:          "Absolute Taboo: Worth Ditching Class For",
		Hrc:           "absolute-taboo-worth-ditching-class-for",
		Description:   "<p>Text.</p>",
		Duration:      3442,
		Cover:         "https://cdn.vrspy.com/videos/207/images/cover.jpg",
		Preview:       "https://cdn.vrspy.com/videos/207/preview.mp4",
		Resolution:    "P8K",
		PublishedDate: 1787381282.300992,
	}
	v.Actors = append(v.Actors, struct {
		Name string `json:"name"`
	}{"Xena Dreamx"}, struct {
		Name string `json:"name"`
	}{"  "})
	v.Tags = append(v.Tags, struct {
		Name string `json:"name"`
	}{"Cumshot"})

	sc := toScene(v, "https://vrspy.com/", now)
	if sc.ID != v.ID || sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("identity = %q/%q/%q", sc.ID, sc.SiteID, sc.Studio)
	}
	if sc.URL != "https://vrspy.com/video/absolute-taboo-worth-ditching-class-for" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Duration != 3442 || sc.Resolution != "8K" {
		t.Errorf("Duration/Resolution = %d/%q", sc.Duration, sc.Resolution)
	}
	if sc.Date.Format("2006-01-02") != "2026-08-22" {
		t.Errorf("Date = %v", sc.Date)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Xena Dreamx" {
		t.Errorf("Performers = %v — a blank name must be dropped", sc.Performers)
	}
	if len(sc.Tags) != 1 || sc.Tags[0] != "Cumshot" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if sc.Description != "Text." {
		t.Errorf("Description = %q", sc.Description)
	}
	if len(sc.PriceHistory) != 0 {
		t.Errorf("PriceHistory = %v, want none for a null price", sc.PriceHistory)
	}
}

// gqlServer answers the three queries this scraper sends, from a fixed catalogue.
func gqlServer(t *testing.T, total int, pages [][]gqlVideo) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("request body: %v", err)
		}

		switch {
		case strings.Contains(req.Query, "videoByHrc"):
			hrc, _ := req.Variables["hrc"].(string)
			mu.Lock()
			seen = append(seen, "detail:"+hrc)
			mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"data":{"videoByHrc":{"price":9.99,"previousPrice":null,"free":false,"tags":[{"name":"Tag %s"}]}}}`, hrc)
		case strings.Contains(req.Query, "videosByActorHrc"):
			mu.Lock()
			seen = append(seen, "actor")
			mu.Unlock()
			writePage(t, w, "videosByActorHrc", total, len(pages), pages[0])
		default:
			page := 0
			if f, ok := req.Variables["page"].(float64); ok {
				page = int(f)
			}
			mu.Lock()
			seen = append(seen, fmt.Sprintf("list:%d", page))
			mu.Unlock()
			if page >= len(pages) {
				writePage(t, w, "videos", total, len(pages), nil)
				return
			}
			writePage(t, w, "videos", total, len(pages), pages[page])
		}
	}))
	return srv, &seen
}

func writePage(t *testing.T, w http.ResponseWriter, field string, total, totalPages int, content []gqlVideo) {
	t.Helper()
	payload := map[string]any{"data": map[string]any{field: map[string]any{
		"content": content, "totalElements": total, "totalPages": totalPages,
	}}}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encoding page: %v", err)
	}
}

func TestListScenes(t *testing.T) {
	pages := [][]gqlVideo{
		{{ID: "a", Name: "One", Hrc: "one", PublishedDate: 1787381282}},
		{{ID: "b", Name: "Two", Hrc: "two", PublishedDate: 1787381281}},
	}
	srv, seen := gqlServer(t, 2, pages)
	defer srv.Close()

	s := New()
	s.api = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://vrspy.com/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	byID := map[string]models.Scene{}
	total := 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			byID[res.Scene.ID] = res.Scene
		case scraper.KindTotal:
			total = res.Total
		case scraper.KindError:
			t.Errorf("error result: %v", res.Err)
		}
	}
	if total != 2 {
		t.Errorf("Total = %d, want 2", total)
	}
	if len(byID) != 2 {
		t.Fatalf("got %d scenes, want 2", len(byID))
	}
	// tags and price come only from the per-scene query.
	sc := byID["a"]
	if len(sc.Tags) != 1 || sc.Tags[0] != "Tag one" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 9.99 {
		t.Errorf("PriceHistory = %v", sc.PriceHistory)
	}
	// The API pages from zero and reports totalPages, so page 2 must not be asked for.
	listCalls := 0
	for _, s := range *seen {
		if strings.HasPrefix(s, "list:") {
			listCalls++
		}
	}
	if listCalls != 2 {
		t.Errorf("list calls = %d (%v), want 2", listCalls, *seen)
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	pages := [][]gqlVideo{
		{{ID: "a", Name: "One", Hrc: "one"}, {ID: "b", Name: "Two", Hrc: "two"}},
		{{ID: "c", Name: "Three", Hrc: "three"}},
	}
	srv, _ := gqlServer(t, 3, pages)
	defer srv.Close()

	s := New()
	s.api = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://vrspy.com/", scraper.ListOpts{KnownIDs: map[string]bool{"b": true}})
	var ids []string
	stopped := false
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			ids = append(ids, res.Scene.ID)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if !stopped {
		t.Error("expected a StoppedEarly result")
	}
	if len(ids) != 1 || ids[0] != "a" {
		t.Errorf("ids = %v", ids)
	}
}

func TestStarURLUsesTheActorQuery(t *testing.T) {
	pages := [][]gqlVideo{{{ID: "a", Name: "One", Hrc: "one"}}}
	srv, seen := gqlServer(t, 1, pages)
	defer srv.Close()

	s := New()
	s.api = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://vrspy.com/star/angel-youngs", scraper.ListOpts{})
	for range ch {
	}
	found := false
	for _, c := range *seen {
		if c == "actor" {
			found = true
		}
	}
	if !found {
		t.Errorf("calls = %v, want the actor query", *seen)
	}
}

func TestGraphQLErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"errors":[{"message":"Validation error"}]}`)
	}))
	defer srv.Close()

	s := New()
	s.api = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://vrspy.com/", scraper.ListOpts{})
	errs := 0
	for res := range ch {
		if res.Kind == scraper.KindError {
			errs++
			if got := scraper.Classify(res.Err); got != scraper.FailureParse {
				t.Errorf("Classify = %v, want FailureParse", got)
			}
		}
	}
	if errs != 1 {
		t.Errorf("errors = %d, want 1", errs)
	}
}
