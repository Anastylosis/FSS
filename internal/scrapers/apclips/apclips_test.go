package apclips

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://apclips.com/ashleymason973", true},
		{"https://www.apclips.com/ashleymason973", true},
		{"https://apclips.com/ashleymason973/videos", true},
		{"https://apclips.com/ashleymason973/videos?sort=date-new", true},
		{"https://apclips.com/ashleymason973/some-clip-slug", false},
		{"https://apclips.com/", false},
		{"https://example.com/apclips", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestSlugFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://apclips.com/ashleymason973", "ashleymason973"},
		{"https://www.apclips.com/ashleymason973/videos", "ashleymason973"},
		{"https://apclips.com/test-creator/videos/", "test-creator"},
	}
	for _, c := range cases {
		if got := slugFromURL(c.url); got != c.want {
			t.Errorf("slugFromURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestParseListingPage(t *testing.T) {
	html := listingHTML("testcreator", []testCard{
		{id: "video-100", title: "First Clip", desc: "Desc one", price: "10", dur: "9:33", thumb: "/ui/img/thumb1.jpg", preview: "https://cdn.example.com/p1.mp4", creator: "Test Creator"},
		{id: "video-200", title: "Second Clip", desc: "Desc two", price: "15", dur: "1:05:30", thumb: "https://cdn.example.com/thumb2.jpg", preview: "https://cdn.example.com/p2.mp4", creator: "Test Creator"},
	}, 2)

	cards, total := parseListingPage([]byte(html))
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}

	c := cards[0]
	if c.id != "video-100" {
		t.Errorf("id = %q", c.id)
	}
	if c.title != "First Clip" {
		t.Errorf("title = %q", c.title)
	}
	if c.description != "Desc one" {
		t.Errorf("description = %q", c.description)
	}
	if c.price != 10 {
		t.Errorf("price = %f", c.price)
	}
	if c.duration != 573 {
		t.Errorf("duration = %d, want 573", c.duration)
	}
	if c.thumbnail != defaultBase+"/ui/img/thumb1.jpg" {
		t.Errorf("thumbnail = %q", c.thumbnail)
	}
	if c.preview != "https://cdn.example.com/p1.mp4" {
		t.Errorf("preview = %q", c.preview)
	}
	if c.creatorName != "Test Creator" {
		t.Errorf("creatorName = %q", c.creatorName)
	}
	if c.detailPath != "/testcreator/first-clip" {
		t.Errorf("detailPath = %q", c.detailPath)
	}

	c2 := cards[1]
	if c2.duration != 3930 {
		t.Errorf("duration (h:m:s) = %d, want 3930", c2.duration)
	}
	if c2.thumbnail != "https://cdn.example.com/thumb2.jpg" {
		t.Errorf("thumbnail (absolute) = %q", c2.thumbnail)
	}
}

func TestParseDetailPage(t *testing.T) {
	html := `<html><body>
<time datetime="Aug 4th, 2024">Aug 4th, 2024</time>
<div class="tag-cloud">
<a class="tag-link" href="/tags/Blonde">Blonde</a>
<a class="tag-link" href="/tags/Blowjob">Blowjob</a>
<a class="tag-link" href="/tags/Outdoors">Outdoors</a>
</div>
</body></html>`

	date, tags := parseDetailPage([]byte(html))
	if date.Year() != 2024 || date.Month() != 8 || date.Day() != 4 {
		t.Errorf("date = %v", date)
	}
	if len(tags) != 3 || tags[0] != "Blonde" || tags[1] != "Blowjob" || tags[2] != "Outdoors" {
		t.Errorf("tags = %v", tags)
	}
}

func TestParseDate(t *testing.T) {
	cases := []struct {
		input string
		month int
		day   int
		year  int
	}{
		{"Aug 4th, 2024", 8, 4, 2024},
		{"Jan 1st, 2025", 1, 1, 2025},
		{"Mar 22nd, 2023", 3, 22, 2023},
		{"Dec 3rd, 2026", 12, 3, 2026},
	}
	for _, c := range cases {
		d := parseDate(c.input)
		if d.IsZero() {
			t.Errorf("parseDate(%q) returned zero", c.input)
			continue
		}
		if int(d.Month()) != c.month || d.Day() != c.day || d.Year() != c.year {
			t.Errorf("parseDate(%q) = %v", c.input, d)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"9:33", 573},
		{"0:45", 45},
		{"1:05:30", 3930},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseutil.ParseDurationColon(c.input); got != c.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestToScene(t *testing.T) {
	c := card{
		id:          "video-12345",
		title:       "Test Scene",
		description: "A description",
		price:       10,
		duration:    573,
		thumbnail:   "https://cdn.example.com/thumb.jpg",
		preview:     "https://cdn.example.com/preview.mp4",
		detailPath:  "/testcreator/test-scene",
		creatorName: "Test Creator",
	}

	sc := toScene(c, "testcreator", "https://apclips.com/testcreator", fixedTime())
	if sc.ID != "video-12345" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != "apclips" {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.URL != "https://apclips.com/testcreator/test-scene" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Title != "Test Scene" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Duration != 573 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if sc.Studio != "Test Creator" {
		t.Errorf("Studio = %q", sc.Studio)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Test Creator" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Preview != "https://cdn.example.com/preview.mp4" {
		t.Errorf("Preview = %q", sc.Preview)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 10 {
		t.Errorf("PriceHistory = %v", sc.PriceHistory)
	}
}

func TestToSceneFallbackStudio(t *testing.T) {
	c := card{
		id:         "video-1",
		title:      "Test",
		detailPath: "/slug/test",
	}
	sc := toScene(c, "myslug", "https://apclips.com/myslug", fixedTime())
	if sc.Studio != "myslug" {
		t.Errorf("Studio = %q, want slug fallback", sc.Studio)
	}
}

type testCard struct {
	id, title, desc, price, dur, thumb, preview, creator string
}

func listingHTML(slug string, cards []testCard, total int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `<html><body><h2 class="h5">Showing 1-<span id="shown">%d</span> of %d Results</h2>`, len(cards), total)
	for _, c := range cards {
		detailSlug := strings.ReplaceAll(strings.ToLower(c.title), " ", "-")
		fmt.Fprintf(&sb, `
<div class="col px-2 pb-1 video-col fix-col">
<div class="card thumb-block with-desc">
<a class="thumb-image" href="/%s/%s" data-content-code="%s" data-content-price="%s" data-preview="%s">
<img class="lazyload" data-src="%s" alt="%s video from %s" />
<span class="item-details">%s</span>
</a>
<a href="/%s/%s">
<span class="item-title break-word">%s</span>
<span class="item-desc break-word">%s</span>
</a>
</div>
</div>`,
			slug, detailSlug,
			c.id, c.price, c.preview,
			c.thumb, c.title, c.creator,
			c.dur,
			slug, detailSlug,
			c.title, c.desc)
	}
	sb.WriteString(`</body></html>`)
	return sb.String()
}

func detailHTML(date string, tags []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `<html><body><time datetime="%s">%s</time>`, date, date)
	sb.WriteString(`<div class="tag-cloud">`)
	for _, tag := range tags {
		fmt.Fprintf(&sb, `<a class="tag-link" href="/tags/%s">%s</a>`, tag, tag)
	}
	sb.WriteString(`</div></body></html>`)
	return sb.String()
}

func newTestServer(slug string, pages [][]testCard, total int, details map[string]string) *httptest.Server {
	pageIdx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Detail page requests. A card with no fixture still gets an empty
		// page rather than a 404, so a test about something else does not have
		// to account for the detail failures that would now be reported.
		if html, ok := details[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, html)
			return
		}

		// Listing page (contains /videos in path).
		if strings.Contains(r.URL.Path, "/videos") {
			if pageIdx >= len(pages) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, `<html><body></body></html>`)
				return
			}
			cards := pages[pageIdx]
			pageIdx++

			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, listingHTML(slug, cards, total))
			return
		}

		if strings.HasPrefix(r.URL.Path, "/"+slug+"/") {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
			return
		}

		http.NotFound(w, r)
	}))
}

func TestListScenes(t *testing.T) {
	slug := "testcreator"
	cards := []testCard{
		{id: "video-1", title: "Scene One", desc: "Desc 1", price: "10", dur: "5:00", thumb: "/thumb1.jpg", preview: "https://cdn/p1.mp4", creator: "Test Creator"},
		{id: "video-2", title: "Scene Two", desc: "Desc 2", price: "15", dur: "10:00", thumb: "/thumb2.jpg", preview: "https://cdn/p2.mp4", creator: "Test Creator"},
	}
	details := map[string]string{
		"/testcreator/scene-one": detailHTML("Aug 4th, 2024", []string{"Tag1", "Tag2"}),
		"/testcreator/scene-two": detailHTML("Sep 1st, 2024", []string{"Tag3"}),
	}
	ts := newTestServer(slug, [][]testCard{cards}, 2, details)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/testcreator", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	if scenes[0].Title != "Scene One" {
		t.Errorf("first scene title = %q", scenes[0].Title)
	}
	if scenes[0].Date.Month() != 8 || scenes[0].Date.Day() != 4 {
		t.Errorf("first scene date = %v", scenes[0].Date)
	}
	if len(scenes[0].Tags) != 2 {
		t.Errorf("first scene tags = %v", scenes[0].Tags)
	}
	if scenes[1].Title != "Scene Two" {
		t.Errorf("second scene title = %q", scenes[1].Title)
	}
}

func TestListScenesPagination(t *testing.T) {
	slug := "testcreator"
	page1 := make([]testCard, perPage)
	for i := range page1 {
		page1[i] = testCard{
			id: fmt.Sprintf("video-%d", i+1), title: fmt.Sprintf("Scene %d", i+1),
			desc: "d", price: "5", dur: "1:00", thumb: "/t.jpg", preview: "", creator: "C",
		}
	}
	page2 := []testCard{
		{id: "video-61", title: "Scene 61", desc: "d", price: "5", dur: "1:00", thumb: "/t.jpg", preview: "", creator: "C"},
		{id: "video-62", title: "Scene 62", desc: "d", price: "5", dur: "1:00", thumb: "/t.jpg", preview: "", creator: "C"},
	}

	ts := newTestServer(slug, [][]testCard{page1, page2}, 62, map[string]string{})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/testcreator", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 62 {
		t.Fatalf("got %d scenes, want 62", len(scenes))
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	slug := "testcreator"
	cards := []testCard{
		{id: "video-1", title: "New", desc: "d", price: "5", dur: "1:00", thumb: "/t.jpg", preview: "", creator: "C"},
		{id: "video-2", title: "Also New", desc: "d", price: "5", dur: "1:00", thumb: "/t.jpg", preview: "", creator: "C"},
		{id: "video-3", title: "Known", desc: "d", price: "5", dur: "1:00", thumb: "/t.jpg", preview: "", creator: "C"},
		{id: "video-4", title: "Old", desc: "d", price: "5", dur: "1:00", thumb: "/t.jpg", preview: "", creator: "C"},
	}

	ts := newTestServer(slug, [][]testCard{cards}, 4, map[string]string{})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/testcreator", scraper.ListOpts{
		KnownIDs: map[string]bool{"video-3": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	if scenes[0].ID != "video-1" || scenes[1].ID != "video-2" {
		t.Errorf("scenes = %v, %v", scenes[0].ID, scenes[1].ID)
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}
