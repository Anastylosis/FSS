package paysite

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func testScraper(base string, client *http.Client) *Scraper {
	return &Scraper{
		cfg: SiteConfig{
			ID:       "testsite",
			SiteBase: base,
			Patterns: []string{"testsite.com"},
			MatchRe:  regexp.MustCompile(`testsite\.com`),
		},
		client: client,
	}
}

func TestMatchesURL(t *testing.T) {
	for _, cfg := range sites {
		s := New(cfg)
		for _, p := range cfg.Patterns {
			url := "https://" + p
			if !s.MatchesURL(url) {
				t.Errorf("%s: MatchesURL(%q) = false, want true", cfg.ID, url)
			}
		}
	}
}

func TestMatchesURLNegative(t *testing.T) {
	for _, cfg := range sites {
		s := New(cfg)
		if s.MatchesURL("https://example.com") {
			t.Errorf("%s: MatchesURL(example.com) should be false", cfg.ID)
		}
	}
}

const nextDataTemplate = `<html><body>
<script id="__NEXT_DATA__" type="application/json">%s</script>
</body></html>`

func makePage(scenes []scene, total, totalPages int) string {
	var items string
	for i, sc := range scenes {
		if i > 0 {
			items += ","
		}
		items += fmt.Sprintf(`{
			"id": %d,
			"title": %q,
			"slug": %q,
			"description": %q,
			"publish_date": %q,
			"seconds_duration": %d,
			"models": [%s],
			"tags": [%s],
			"thumb": %q,
			"trailer_screencap": %q,
			"site": "TestSite",
			"videos": {"orig": {"width": %d, "height": %d}},
			"rating": 0
		}`,
			sc.ID, sc.Title, sc.Slug, sc.Description, sc.PublishDate,
			sc.SecondsDuration, quotedList(sc.Models), quotedList(sc.Tags),
			sc.Thumb, sc.TrailerScreencap,
			1920, 1080,
		)
	}
	payload := fmt.Sprintf(`{"props":{"pageProps":{"contents":{"total":%d,"page":"1","total_pages":%d,"data":[%s]}}}}`,
		total, totalPages, items)
	return fmt.Sprintf(nextDataTemplate, payload)
}

func quotedList(ss []string) string {
	var parts string
	for i, s := range ss {
		if i > 0 {
			parts += ","
		}
		parts += fmt.Sprintf("%q", s)
	}
	return parts
}

func TestParsePage(t *testing.T) {
	html := makePage([]scene{
		{ID: 100, Title: "Test Scene", Slug: "test-scene", PublishDate: "2024/03/15 12:00:00", SecondsDuration: 600, Models: []string{"Model"}, Tags: []string{"JOI"}},
	}, 50, 3)

	contents, err := parsePage([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	if contents.Total != 50 {
		t.Errorf("Total = %d, want 50", contents.Total)
	}
	if contents.TotalPages != 3 {
		t.Errorf("TotalPages = %d, want 3", contents.TotalPages)
	}
	if len(contents.Data) != 1 || contents.Data[0].Title != "Test Scene" {
		t.Fatalf("unexpected Data: %v", contents.Data)
	}
}

func TestToScene(t *testing.T) {
	sc := scene{
		ID:               200,
		Title:            "A Test",
		Slug:             "a-test",
		Description:      "&amp;nbsp;Description&nbsp;text",
		PublishDate:      "2024/04/05 12:00:00",
		SecondsDuration:  518,
		Models:           []string{"Model A", "Model B"},
		Tags:             []string{"Big Tits", "JOI"},
		Thumb:            "https://cdn.example.com/thumb.jpg",
		TrailerScreencap: "https://cdn.example.com/screenshot.jpg",
		Site:             "TestSite",
		Videos:           map[string]videoFormat{"orig": {Width: 1920, Height: 1080}},
	}

	result := toScene("testsite", "https://tour.testsite.com", "https://tour.testsite.com/x", sc)

	if result.StudioURL != "https://tour.testsite.com/x" {
		t.Errorf("StudioURL = %q", result.StudioURL)
	}
	if result.ID != "200" {
		t.Errorf("ID = %q", result.ID)
	}
	if result.SiteID != "testsite" {
		t.Errorf("SiteID = %q", result.SiteID)
	}
	if result.URL != "https://tour.testsite.com/scenes/a-test" {
		t.Errorf("URL = %q", result.URL)
	}
	if result.Studio != "TestSite" {
		t.Errorf("Studio = %q", result.Studio)
	}
	if result.Duration != 518 {
		t.Errorf("Duration = %d", result.Duration)
	}
	if result.Thumbnail != "https://cdn.example.com/screenshot.jpg" {
		t.Errorf("Thumbnail = %q", result.Thumbnail)
	}
	if result.Width != 1920 || result.Height != 1080 {
		t.Errorf("Width=%d Height=%d", result.Width, result.Height)
	}
	if result.Resolution != "1080p" {
		t.Errorf("Resolution = %q", result.Resolution)
	}
	wantDate := time.Date(2024, 4, 5, 12, 0, 0, 0, time.UTC)
	if !result.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", result.Date, wantDate)
	}
	if len(result.Performers) != 2 {
		t.Errorf("Performers = %v", result.Performers)
	}
	if len(result.Tags) != 2 {
		t.Errorf("Tags = %v", result.Tags)
	}
	if result.Description != "&nbsp;Description text" {
		t.Errorf("Description = %q", result.Description)
	}
}

func TestToSceneTrailingSpaceSite(t *testing.T) {
	sc := scene{ID: 1, Slug: "x", Site: "MariskaX "}
	result := toScene("mariskax", "https://tour.mariskax.com", "https://tour.mariskax.com/x", sc)
	if result.Studio != "MariskaX" {
		t.Errorf("Studio = %q, want trimmed", result.Studio)
	}
}

func TestToSceneThumbFallback(t *testing.T) {
	sc := scene{ID: 300, Slug: "fallback", Thumb: "https://cdn.example.com/thumb.jpg"}
	result := toScene("testsite", "https://tour.testsite.com", "https://tour.testsite.com/x", sc)
	if result.Thumbnail != "https://cdn.example.com/thumb.jpg" {
		t.Errorf("Thumbnail = %q, want thumb fallback", result.Thumbnail)
	}
}

func TestListScenes(t *testing.T) {
	scenes := []scene{
		{ID: 1, Title: "Scene A", Slug: "scene-a", PublishDate: "2024/01/01 12:00:00", Models: []string{"Model"}, Tags: []string{"Tag1"}},
		{ID: 2, Title: "Scene B", Slug: "scene-b", PublishDate: "2024/01/02 12:00:00", Models: []string{"Model"}, Tags: []string{"Tag2"}},
	}
	page := makePage(scenes, 2, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, page)
	}))
	defer ts.Close()

	s := testScraper(ts.URL, ts.Client())
	studioURL := ts.URL + "/studio"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
	for _, r := range results {
		if r.StudioURL != studioURL {
			t.Errorf("StudioURL = %q, want %q", r.StudioURL, studioURL)
		}
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	scenes := []scene{
		{ID: 10, Title: "New", Slug: "new", PublishDate: "2024/02/01 12:00:00"},
		{ID: 11, Title: "Known", Slug: "known", PublishDate: "2024/01/01 12:00:00"},
	}
	page := makePage(scenes, 2, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, page)
	}))
	defer ts.Close()

	s := testScraper(ts.URL, ts.Client())
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"11": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
	if len(results) != 1 || results[0].ID != "10" {
		t.Errorf("got IDs %v, want [10]", results)
	}
}

// Config-only wrapper: parsing tests live in the *util it delegates to. What is
// not covered there is this table's own integrity — see testutil.CheckSiteTable.
func TestSiteTableIntegrity(t *testing.T) {
	// This config carries no studio/name field; CheckSiteTable skips the
	// Studio check when no row sets one.
	rows := make([]testutil.SiteRow, 0, len(sites))
	for _, c := range sites {
		rows = append(rows, testutil.SiteRow{
			ID: c.ID, Base: c.SiteBase,
			Patterns: c.Patterns, MatchRe: c.MatchRe,
		})
	}
	testutil.CheckSiteTable(t, rows)
	testutil.CheckSiteTableDomains(t, rows)
}
