package ifeelmyself

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func buildIFMPage(items []struct {
	sceneID, price, artistID, performer, title, duration, date, thumb string
	categories, tags                                                  []string
}) string {
	html := ""
	for _, it := range items {
		catHTML := ""
		if len(it.categories) > 0 {
			catHTML = `<b>Categories:</b><table>`
			for _, c := range it.categories {
				catHTML += fmt.Sprintf(`<td>%s</td>`, c)
			}
			catHTML += `</table>`
		}
		tagHTML := ""
		for _, tg := range it.tags {
			tagHTML += fmt.Sprintf(`<span class="tags-list-item-tag">%s</span>`, tg)
		}
		titleHTML := ""
		if it.title != "" {
			titleHTML = fmt.Sprintf(`&nbsp;in&nbsp;"%s"`, it.title)
		}
		thumbHTML := ""
		if it.thumb != "" {
			thumbHTML = fmt.Sprintf(`<img SRC='%s' HEIGHT='90' BORDER='0'>`, it.thumb)
		}
		durationHTML := ""
		if it.duration != "" {
			durationHTML = fmt.Sprintf(`HD Video, %s min`, it.duration)
		}

		html += fmt.Sprintf(`<div class="ThumbRec">
			<table class="ThumbTab ppss-scene" data-scene-id="%s" data-scene-price="%s">
			<b><a href='javascript:openLink2(0,artist_bio&amp;artist_id=%s')'>%s</a></b>
			%s
			%s
			%s
			%s
			%s
			%s
			</TABLE>
		</div>`, it.sceneID, it.price, it.artistID, it.performer, titleHTML, durationHTML, it.date, thumbHTML, catHTML, tagHTML)
	}
	return html
}

func TestParseListingPage(t *testing.T) {
	html := buildIFMPage([]struct {
		sceneID, price, artistID, performer, title, duration, date, thumb string
		categories, tags                                                  []string
	}{
		{
			sceneID:    "12345",
			price:      "1.95",
			artistID:   "ABC123",
			performer:  "Luna",
			title:      "Morning Light",
			duration:   "9:48",
			date:       "08 May 2026",
			thumb:      "https://bcdn.ifeelmyself.com/img/12345.jpg",
			categories: []string{"Solo", "Outdoor"},
			tags:       []string{"blonde", "natural"},
		},
		{
			sceneID:   "12344",
			price:     "0",
			artistID:  "DEF456",
			performer: "Stella",
			title:     "",
			duration:  "5:30",
			date:      "01 May 2026",
			thumb:     "https://bcdn.ifeelmyself.com/img/12344.jpg",
		},
	})

	scenes := parseListingPage([]byte(html), "https://ifeelmyself.com")
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s := scenes[0]
	if s.ID != "12345/ABC123" {
		t.Errorf("ID = %q, want 12345/ABC123", s.ID)
	}
	if s.Title != "Morning Light" {
		t.Errorf("Title = %q, want %q", s.Title, "Morning Light")
	}
	if len(s.Performers) != 1 || s.Performers[0] != "Luna" {
		t.Errorf("Performers = %v, want [Luna]", s.Performers)
	}
	if s.Duration != 9*60+48 {
		t.Errorf("Duration = %d, want %d", s.Duration, 9*60+48)
	}
	if s.Date.Format("2006-01-02") != "2026-05-08" {
		t.Errorf("Date = %v, want 2026-05-08", s.Date)
	}
	if s.Thumbnail != "https://bcdn.ifeelmyself.com/img/12345.jpg" {
		t.Errorf("Thumbnail = %q", s.Thumbnail)
	}
	if len(s.Categories) != 2 {
		t.Errorf("Categories = %v, want 2 items", s.Categories)
	}
	if len(s.Tags) != 2 {
		t.Errorf("Tags = %v, want 2 items", s.Tags)
	}
	if s.SiteID != "ifeelmyself" {
		t.Errorf("SiteID = %q, want ifeelmyself", s.SiteID)
	}
	if s.Studio != "I Feel Myself" {
		t.Errorf("Studio = %q, want %q", s.Studio, "I Feel Myself")
	}
	if s.LowestPrice != 1.95 {
		t.Errorf("LowestPrice = %f, want 1.95", s.LowestPrice)
	}

	s2 := scenes[1]
	if s2.Title != "Scene #12344" {
		t.Errorf("Title = %q, want %q", s2.Title, "Scene #12344")
	}
	if s2.LowestPrice != 0 {
		t.Errorf("LowestPrice = %f, want 0 (free)", s2.LowestPrice)
	}
}

func TestParseListingPageEmpty(t *testing.T) {
	scenes := parseListingPage([]byte(`<div>no results</div>`), "https://ifeelmyself.com")
	if len(scenes) != 0 {
		t.Fatalf("got %d scenes, want 0", len(scenes))
	}
}

func TestSplitBlocks(t *testing.T) {
	html := `<div class="ThumbRec">block1</div><div class="ThumbRec">block2</div>`
	blocks := splitBlocks([]byte(html))
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
}

func TestKnownIDsEarlyStop(t *testing.T) {
	html := buildIFMPage([]struct {
		sceneID, price, artistID, performer, title, duration, date, thumb string
		categories, tags                                                  []string
	}{
		{sceneID: "100", price: "0", artistID: "A", performer: "X", date: "01 Jan 2021"},
		{sceneID: "99", price: "0", artistID: "B", performer: "Y", date: "31 Dec 2020"},
		{sceneID: "98", price: "0", artistID: "C", performer: "Z", date: "30 Dec 2020"},
	})

	scenes := parseListingPage([]byte(html), "https://ifeelmyself.com")
	known := map[string]bool{"99/B": true}
	var collected []string
	for _, sc := range scenes {
		if known[sc.ID] {
			break
		}
		collected = append(collected, sc.ID)
	}
	if len(collected) != 1 || collected[0] != "100/A" {
		t.Errorf("collected = %v, want [100/A]", collected)
	}
}

// The live listing runs one card per artist appearing in a film, so several
// cards carry the same data-scene-id while being different scenes — three
// cards shared 24195 on the front page, two of them different titles. Keyed
// on the film id alone they collided on (ID, SiteID) and overwrote each other
// in the store.
func TestSceneIDIsUniquePerArtistOnAFilm(t *testing.T) {
	html := buildIFMPage([]struct {
		sceneID, price, artistID, performer, title, duration, date, thumb string
		categories, tags                                                  []string
	}{
		{sceneID: "24195", price: "1.95", artistID: "F16959", performer: "Natalia_J", title: "pleasure land 1", date: "19 Aug 2026"},
		{sceneID: "24195", price: "1.95", artistID: "F16642", performer: "Melanie_J", title: "pleasure land 1", date: "19 Aug 2026"},
		{sceneID: "24195", price: "1.95", artistID: "F15003", performer: "Skye_C", title: "orgasm express", date: "18 Aug 2026"},
	})

	scenes := parseListingPage([]byte(html), "https://ifeelmyself.com")
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	seen := map[string]string{}
	for _, sc := range scenes {
		if prev, dup := seen[sc.ID]; dup {
			t.Errorf("ID %q reused by %q and %q", sc.ID, prev, sc.Title)
		}
		seen[sc.ID] = sc.Title
	}
	if _, ok := seen["24195/F16959"]; !ok {
		t.Errorf("IDs = %v, want one keyed 24195/F16959", seen)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://ifeelmyself.com", true},
		{"https://www.ifeelmyself.com", true},
		{"http://ifeelmyself.com/public/main.php", true},
		{"https://ifeelmyself.com/public/main.php?page=artist_bio&artist_id=ABC", true},
		{"https://beautifulagony.com", false},
		{"https://example.com", false},
	}
	for _, tc := range tests {
		if got := s.MatchesURL(tc.url); got != tc.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestPatterns(t *testing.T) {
	s := New()
	pats := s.Patterns()
	if len(pats) < 1 {
		t.Fatal("no patterns")
	}
}

func TestInterface(t *testing.T) {
	var s scraper.StudioScraper = New()
	_ = s
}

func TestExtractSearchKeyword_present(t *testing.T) {
	got := extractSearchKeyword("https://ifeelmyself.com/public/main.php?page=quick_search&keyword=Alice")
	if got != "Alice" {
		t.Errorf("extractSearchKeyword = %q, want %q", got, "Alice")
	}
}

func TestExtractSearchKeyword_encoded(t *testing.T) {
	got := extractSearchKeyword("https://ifeelmyself.com/public/main.php?page=quick_search&keyword=Jane%20Doe")
	if got != "Jane Doe" {
		t.Errorf("extractSearchKeyword = %q, want %q", got, "Jane Doe")
	}
}

func TestExtractSearchKeyword_missing(t *testing.T) {
	got := extractSearchKeyword("https://ifeelmyself.com/public/main.php?page=view&mode=all")
	if got != "" {
		t.Errorf("extractSearchKeyword = %q, want empty", got)
	}
}

func TestExtractSearchKeyword_empty(t *testing.T) {
	got := extractSearchKeyword("https://ifeelmyself.com/public/main.php?page=quick_search&keyword=")
	if got != "" {
		t.Errorf("extractSearchKeyword = %q, want empty", got)
	}
}

func TestSceneValidation(t *testing.T) {
	html := buildIFMPage([]struct {
		sceneID, price, artistID, performer, title, duration, date, thumb string
		categories, tags                                                  []string
	}{
		{
			sceneID:   "12345",
			price:     "1.95",
			artistID:  "ABC123",
			performer: "Luna",
			title:     "Morning Light",
			duration:  "9:48",
			date:      "08 May 2026",
			thumb:     "https://bcdn.ifeelmyself.com/img/12345.jpg",
		},
	})
	scenes := parseListingPage([]byte(html), "https://ifeelmyself.com")
	if len(scenes) != 1 {
		t.Fatal("expected 1 scene")
	}
	testutil.ValidateScene(t, scenes[0])
}

// The listing offsets by film but renders one card per artist, so a film with
// several artists pushes the window out of step and the last card of one page
// comes back as the first card of the next. Live, 24205/f16944 arrived twice.
func TestPaginationOverlapEmitsEachSceneOnce(t *testing.T) {
	type item = struct {
		sceneID, price, artistID, performer, title, duration, date, thumb string
		categories, tags                                                  []string
	}

	overlap := item{sceneID: "205", price: "0", artistID: "F944", performer: "Overlap"}

	first := make([]item, 0, pageSize)
	for i := 0; i < pageSize-1; i++ {
		first = append(first, item{
			sceneID:   fmt.Sprintf("3%02d", i),
			price:     "0",
			artistID:  fmt.Sprintf("A%02d", i),
			performer: "Performer",
		})
	}
	first = append(first, overlap)

	second := []item{
		overlap,
		{sceneID: "204", price: "0", artistID: "B1", performer: "Q"},
		{sceneID: "203", price: "0", artistID: "B2", performer: "R"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("offset") {
		case "0":
			_, _ = fmt.Fprint(w, buildIFMPage(first))
		case "12":
			_, _ = fmt.Fprint(w, buildIFMPage(second))
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer srv.Close()

	restore := siteBase
	siteBase = srv.URL
	defer func() { siteBase = restore }()

	s := New()
	s.client = srv.Client()

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	counts := map[string]int{}
	total := 0
	for res := range ch {
		if res.Kind != scraper.KindScene {
			continue
		}
		total++
		counts[res.Scene.ID]++
	}

	if n := counts["205/F944"]; n != 1 {
		t.Errorf("overlapping scene emitted %d times, want 1", n)
	}
	if want := pageSize + 2; total != want {
		t.Errorf("emitted %d scenes, want %d (the repeated card counted once)", total, want)
	}
}
