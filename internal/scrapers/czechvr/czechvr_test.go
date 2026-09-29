package czechvr

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

var _ scraper.StudioScraper = (*siteScraper)(nil)

func TestFindPostBlocks(t *testing.T) {
	t.Run("splits blocks correctly", func(t *testing.T) {
		page := `<a name="post1"></a>block one content<a name="post2"></a>block two content<a name="post3"></a>block three content`
		blocks := findPostBlocks(page)
		if len(blocks) != 3 {
			t.Fatalf("expected 3 blocks, got %d", len(blocks))
		}
		if blocks[0] != `<a name="post1"></a>block one content` {
			t.Errorf("block[0] = %q", blocks[0])
		}
		if blocks[1] != `<a name="post2"></a>block two content` {
			t.Errorf("block[1] = %q", blocks[1])
		}
		if blocks[2] != `<a name="post3"></a>block three content` {
			t.Errorf("block[2] = %q", blocks[2])
		}
	})

	t.Run("single block extends to end", func(t *testing.T) {
		page := `preamble<a name="post42"></a>the only block`
		blocks := findPostBlocks(page)
		if len(blocks) != 1 {
			t.Fatalf("expected 1 block, got %d", len(blocks))
		}
		if blocks[0] != `<a name="post42"></a>the only block` {
			t.Errorf("block[0] = %q", blocks[0])
		}
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		blocks := findPostBlocks("")
		if blocks != nil {
			t.Fatalf("expected nil, got %v", blocks)
		}
	})

	t.Run("no anchors returns nil", func(t *testing.T) {
		blocks := findPostBlocks("<div>no post anchors here</div>")
		if blocks != nil {
			t.Fatalf("expected nil, got %v", blocks)
		}
	})
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  time.Time
	}{
		{"full month single-digit day", "January 2, 2024", time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
		{"abbreviated month single-digit day", "Mar 5, 2023", time.Date(2023, 3, 5, 0, 0, 0, 0, time.UTC)},
		{"full month zero-padded day", "December 05, 2022", time.Date(2022, 12, 5, 0, 0, 0, 0, time.UTC)},
		{"abbreviated month zero-padded day", "Sep 09, 2021", time.Date(2021, 9, 9, 0, 0, 0, 0, time.UTC)},
		{"with leading/trailing whitespace", "  June 15, 2020  ", time.Date(2020, 6, 15, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDate(tt.input)
			if !got.Equal(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("invalid date returns zero", func(t *testing.T) {
		got := parseDate("not-a-date")
		if !got.IsZero() {
			t.Fatalf("expected zero time for invalid date, got %v", got)
		}
	})

	t.Run("empty string returns zero", func(t *testing.T) {
		got := parseDate("")
		if !got.IsZero() {
			t.Fatalf("expected zero time for empty string, got %v", got)
		}
	})
}

func TestStripTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"removes simple tags", "<b>bold</b> text", "bold text"},
		{"removes nested tags", "<div><span>hello</span></div>", "hello"},
		{"removes self-closing tags", "before<br/>after", "beforeafter"},
		{"removes tags with attributes", `<a href="url">link</a>`, "link"},
		{"no tags", "plain text", "plain text"},
		{"empty input", "", ""},
		{"trims whitespace", "  <b>text</b>  ", "text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripTags(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveURL(t *testing.T) {
	s := newSiteScraper(sites[0]) // czechvrnetwork.com
	base := "https://www.czechvrnetwork.com"

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"dot-slash relative", "./images/thumb.jpg", base + "/images/thumb.jpg"},
		{"absolute path", "/detail-123-scene", base + "/detail-123-scene"},
		{"bare path", "detail-456-scene", base + "/detail-456-scene"},
		{"full http URL", "http://cdn.example.com/img.jpg", "http://cdn.example.com/img.jpg"},
		{"full https URL", "https://cdn.example.com/img.jpg", "https://cdn.example.com/img.jpg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.resolveURL(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseListingCards(t *testing.T) {
	s := newSiteScraper(sites[0])
	base := "https://www.czechvrnetwork.com"

	page := `<html>
<a name="post100"></a>
<div class="card">
  <a href="./detail-42-my-scene-title">
  <h2><a href="./detail-42-my-scene-title">My Scene Title</a></h2>
  <div class="featuring"><a href="./model-alice">Alice</a>, <a href="./model-bob">Bob</a></div>
  <span class="datum">March 15, 2024</span><
  <span class="cas"><span class="icon">30:45</span>
  <img data-src="./thumbs/42.jpg" />
  <video><source src="https://preview.cdn.com/42.mp4"></video>
</div>
<a name="post200"></a>
<div class="card">
  <a href="./detail-99-another-scene">
  <h2><a href="./detail-99-another-scene">Another Scene</a></h2>
  <div class="featuring"><a href="./model-carol">Carol</a></div>
  <span class="datum">Apr 1, 2025</span><
  <span class="cas"><span class="time">15:30</span>
  <img data-src="/thumbs/99.jpg" />
</div>
</html>`

	items := s.parseListingCards(page)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	first := items[0]
	if first.id != "42" {
		t.Errorf("id = %q, want %q", first.id, "42")
	}
	if first.url != base+"/detail-42-my-scene-title" {
		t.Errorf("url = %q", first.url)
	}
	if first.title != "My Scene Title" {
		t.Errorf("title = %q, want %q", first.title, "My Scene Title")
	}
	if len(first.performers) != 2 || first.performers[0] != "Alice" || first.performers[1] != "Bob" {
		t.Errorf("performers = %v, want [Alice Bob]", first.performers)
	}
	wantDate := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	if !first.date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", first.date, wantDate)
	}
	if first.duration != 30*60+45 {
		t.Errorf("duration = %d, want %d", first.duration, 30*60+45)
	}
	if first.thumb != base+"/thumbs/42.jpg" {
		t.Errorf("thumb = %q", first.thumb)
	}
	if first.preview != "https://preview.cdn.com/42.mp4" {
		t.Errorf("preview = %q", first.preview)
	}

	second := items[1]
	if second.id != "99" {
		t.Errorf("second id = %q, want %q", second.id, "99")
	}
	if second.title != "Another Scene" {
		t.Errorf("second title = %q", second.title)
	}
	if len(second.performers) != 1 || second.performers[0] != "Carol" {
		t.Errorf("second performers = %v", second.performers)
	}
	if second.thumb != base+"/thumbs/99.jpg" {
		t.Errorf("second thumb = %q", second.thumb)
	}
	if second.preview != "" {
		t.Errorf("second preview = %q, want empty", second.preview)
	}
}

func TestParseListingCards_deduplication(t *testing.T) {
	s := newSiteScraper(sites[0])

	page := `
<a name="post1"></a>
<a href="./detail-42-some-scene"><h2><a href="./detail-42-some-scene">First</a></h2>
<a name="post2"></a>
<a href="./detail-42-some-scene"><h2><a href="./detail-42-some-scene">Duplicate</a></h2>
<a name="post3"></a>
<a href="./detail-99-other-scene"><h2><a href="./detail-99-other-scene">Other</a></h2>`

	items := s.parseListingCards(page)
	if len(items) != 2 {
		t.Fatalf("expected 2 items after dedup, got %d", len(items))
	}
	if items[0].id != "42" {
		t.Errorf("first id = %q, want %q", items[0].id, "42")
	}
	if items[0].title != "First" {
		t.Errorf("first title = %q, want %q", items[0].title, "First")
	}
	if items[1].id != "99" {
		t.Errorf("second id = %q, want %q", items[1].id, "99")
	}
}

func TestMatchesURL(t *testing.T) {
	tests := []struct {
		name   string
		siteID string
		url    string
		want   bool
	}{
		{"czechvrnetwork bare domain", "czechvrnetwork", "https://czechvrnetwork.com", true},
		{"czechvrnetwork www", "czechvrnetwork", "https://www.czechvrnetwork.com/vr-porn-videos", true},
		{"czechvr", "czechvr", "https://www.czechvr.com/detail-100-scene", true},
		{"czechvrfetish", "czechvrfetish", "https://czechvrfetish.com/model-alice", true},
		{"czechvrcasting", "czechvrcasting", "https://www.czechvrcasting.com", true},
		{"vrintimacy", "vrintimacy", "https://vrintimacy.com/tag-something", true},
		{"czechar", "czechar", "http://www.czechar.com/vr-porn-videos", true},
		{"wrong domain", "czechvr", "https://www.example.com", false},
		{"partial domain mismatch", "czechvr", "https://www.czechvrfetish.com", false},
	}

	scraperMap := map[string]*siteScraper{}
	for _, cfg := range sites {
		scraperMap[cfg.SiteID] = newSiteScraper(cfg)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := scraperMap[tt.siteID]
			if !ok {
				t.Fatalf("no scraper for site %q", tt.siteID)
			}
			got := s.MatchesURL(tt.url)
			if got != tt.want {
				t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

const detailFixtureHTML = `<html>
<h1>Czech VR 456 - Amazing Scene</h1>
<div class="datum">January 15, 2026</div>
<div class="cas">25:30</div>
<div class="text">A great description here</div>
<div class="modelky"><a href="#"><span>Jane Doe</span></a><a href="#"><span>John Smith</span></a><div class="cistic"></div></div>
<div id="Tagy"><div><a href="#">VR</a></div><div><a href="#">Blonde</a></div><div class="cistic"></div></div>
<video poster="/thumbs/456.jpg"></video>
<source src="https://preview.czechvr.com/456.mp4">
</html>`

func newTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, detailFixtureHTML)
	}))
}

func TestFetchDetail_fullPage(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newSiteScraper(sites[0])
	s.client = ts.Client()

	item := workItem{
		id:  "456",
		url: ts.URL + "/detail-456-amazing-scene",
	}

	scene, err := s.fetchDetail(context.Background(), item, "https://czechvrnetwork.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if scene.ID != "456" {
		t.Errorf("ID = %q, want %q", scene.ID, "456")
	}
	if scene.Title != "Amazing Scene" {
		t.Errorf("Title = %q, want %q", scene.Title, "Amazing Scene")
	}
	wantDate := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", scene.Date, wantDate)
	}
	if scene.Duration != 25*60+30 {
		t.Errorf("Duration = %d, want %d", scene.Duration, 25*60+30)
	}
	if scene.Description != "A great description here" {
		t.Errorf("Description = %q, want %q", scene.Description, "A great description here")
	}
	if len(scene.Performers) != 2 {
		t.Fatalf("Performers len = %d, want 2", len(scene.Performers))
	}
	if scene.Performers[0] != "Jane Doe" {
		t.Errorf("Performers[0] = %q, want %q", scene.Performers[0], "Jane Doe")
	}
	if scene.Performers[1] != "John Smith" {
		t.Errorf("Performers[1] = %q, want %q", scene.Performers[1], "John Smith")
	}
	if len(scene.Tags) != 2 {
		t.Fatalf("Tags len = %d, want 2", len(scene.Tags))
	}
	if scene.Tags[0] != "VR" {
		t.Errorf("Tags[0] = %q, want %q", scene.Tags[0], "VR")
	}
	if scene.Tags[1] != "Blonde" {
		t.Errorf("Tags[1] = %q, want %q", scene.Tags[1], "Blonde")
	}
	wantThumb := "https://www.czechvrnetwork.com/thumbs/456.jpg"
	if scene.Thumbnail != wantThumb {
		t.Errorf("Thumbnail = %q, want %q", scene.Thumbnail, wantThumb)
	}
	if scene.Preview != "https://preview.czechvr.com/456.mp4" {
		t.Errorf("Preview = %q, want %q", scene.Preview, "https://preview.czechvr.com/456.mp4")
	}
	if scene.SiteID != "czechvrnetwork" {
		t.Errorf("SiteID = %q, want %q", scene.SiteID, "czechvrnetwork")
	}
	if scene.StudioURL != "https://czechvrnetwork.com" {
		t.Errorf("StudioURL = %q, want %q", scene.StudioURL, "https://czechvrnetwork.com")
	}
	if scene.Studio != "CzechVR Network" {
		t.Errorf("Studio = %q, want %q", scene.Studio, "CzechVR Network")
	}
	if scene.ScrapedAt.IsZero() {
		t.Error("ScrapedAt is zero")
	}
}

func TestFetchDetail_prefilled(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newSiteScraper(sites[0])
	s.client = ts.Client()

	preDate := time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC)
	item := workItem{
		id:         "456",
		url:        ts.URL + "/detail-456-amazing-scene",
		title:      "Prefilled Title",
		date:       preDate,
		duration:   999,
		performers: []string{"Already Here"},
		thumb:      "https://example.com/existing.jpg",
		preview:    "https://example.com/existing.mp4",
	}

	scene, err := s.fetchDetail(context.Background(), item, "https://czechvrnetwork.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if scene.Title != "Prefilled Title" {
		t.Errorf("Title = %q, want %q (should not be overwritten)", scene.Title, "Prefilled Title")
	}
	if !scene.Date.Equal(preDate) {
		t.Errorf("Date = %v, want %v (should not be overwritten)", scene.Date, preDate)
	}
	if scene.Duration != 999 {
		t.Errorf("Duration = %d, want 999 (should not be overwritten)", scene.Duration)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Already Here" {
		t.Errorf("Performers = %v, want [Already Here] (should not be overwritten)", scene.Performers)
	}
	if scene.Thumbnail != "https://example.com/existing.jpg" {
		t.Errorf("Thumbnail = %q, want existing (should not be overwritten)", scene.Thumbnail)
	}
	if scene.Preview != "https://example.com/existing.mp4" {
		t.Errorf("Preview = %q, want existing (should not be overwritten)", scene.Preview)
	}
	// Description is always parsed from the page (not conditional on empty).
	if scene.Description != "A great description here" {
		t.Errorf("Description = %q, want %q", scene.Description, "A great description here")
	}
	// Tags are always parsed from the page (not conditional on empty).
	if len(scene.Tags) != 2 {
		t.Errorf("Tags len = %d, want 2", len(scene.Tags))
	}
}

func TestFetchDetail_titleStripsPrefix(t *testing.T) {
	tests := []struct {
		name      string
		h1        string
		wantTitle string
	}{
		{"standard prefix", `<h1>Czech VR 123 - My Title</h1>`, "My Title"},
		{"prefix with tags", `<h1><span>Czech VR 789</span> - Tagged Title</h1>`, "Tagged Title"},
		{"no prefix", `<h1>No Dash Title</h1>`, "No Dash Title"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := fmt.Sprintf(`<html>%s
<div class="datum">January 1, 2025</div>
<div class="cas">10:00</div>
<div class="text">desc</div>
</html>`, tt.h1)

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprint(w, page)
			}))
			defer ts.Close()

			s := newSiteScraper(sites[0])
			s.client = ts.Client()

			item := workItem{
				id:  "123",
				url: ts.URL + "/detail-123-test",
			}

			scene, err := s.fetchDetail(context.Background(), item, "https://czechvrnetwork.com", 0)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if scene.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", scene.Title, tt.wantTitle)
			}
		})
	}
}

// A model page paginates by item offset (`?&next=19`) and renders no pager at
// all when the performer fits on one page. The walk used to fetch exactly one
// page: live, Alexis Crystal's 41 scenes arrive 24 then 17.
func TestScrapeModelPageFollowsThePager(t *testing.T) {
	card := func(id, title string) string {
		return fmt.Sprintf(`<a name="post%s"></a><div class="card">
  <a href="./detail-%s-%s">
  <h2><a href="./detail-%s-%s">%s</a></h2>
  <span class="datum">March 15, 2024</span><
</div>`, id, id, title, id, title, title)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.RawQuery {
		case "":
			_, _ = fmt.Fprint(w, "<html>"+card("1", "one")+card("2", "two")+
				`<a class="next" href="./model-alice?&amp;next=3" text=""></a></html>`)
		case "&next=3":
			// The last page: three more scenes and no pager.
			_, _ = fmt.Fprint(w, "<html>"+card("3", "three")+"</html>")
		default:
			t.Errorf("unexpected fetch %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := newSiteScraper(sites[0])
	s.client = srv.Client()

	out := make(chan scraper.SceneResult, 16)
	work := make(chan workItem, 16)
	s.scrapeModelPage(context.Background(), srv.URL+"/model-alice", scraper.ListOpts{}, out, work)
	close(work)
	close(out)

	var ids []string
	for it := range work {
		ids = append(ids, it.id)
	}
	if len(ids) != 3 {
		t.Fatalf("queued %v, want all three scenes across both pages", ids)
	}
	for r := range out {
		if r.Kind == scraper.KindError {
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
}
