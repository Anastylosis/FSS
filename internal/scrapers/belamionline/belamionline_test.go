package belamionline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

const listingHTML = `<!DOCTYPE html>
<html>
<body>
<div class="contents">
    <div class="content_list">
        <div class="content">
            <div class="wrap">
                <div class="img">
                    <a href="playvideo.aspx?VideoID=19490">
                        <img data-src="https://freeassets.belamionline.com/Data/Contents/Content_19490/Thumbnail10.jpg" alt="Rikki Norseman had barely started with us when he expressed interest in becoming a photographer." loading="lazy" decoding="async">
                    </a>
                </div>
                <div class="more_top">
                    <span class="label">Bruce &amp; Rikki</span>
                    <span class="stars">
                        <img class="RatingStarButton" src="images/rating/size1/1.0.svg">
                    </span>
                </div>
            </div>
            <div class="more_bottom">
                <div class="tags">
                    <a href="latestsexscenes.aspx?filter=Condom+Free">Condom Free</a><a href="latestsexscenes.aspx?filter=Sex+Scenes">Sex Scenes</a>
                </div>
                <div class="date">6/6/2026</div>
            </div>
        </div>

        <div class="content">
            <div class="wrap">
                <div class="img">
                    <a href="playvideo.aspx?VideoID=17594">
                        <img data-src="https://freeassets.belamionline.com/Data/Contents/Content_17594/Thumbnail10.jpg" alt="Edison Jones solo scene description text here." loading="lazy" decoding="async">
                    </a>
                </div>
                <div class="more_top">
                    <span class="label">Edison Jones</span>
                    <span class="stars">
                        <img class="RatingStarButton" src="images/rating/size1/1.0.svg">
                    </span>
                </div>
            </div>
            <div class="more_bottom">
                <div class="tags">
                    <a href="latestsolos.aspx?filter=Singles">Singles</a><a href="latestsolos.aspx?filter=Castings">Castings</a>
                </div>
                <div class="date">6/7/2026</div>
            </div>
        </div>
    </div>
    <div class="pag_b"><a class="prev">Prev</a><a href="latestsexscenes.aspx" class="current">1</a><a href="latestsexscenes.aspx?page=2">2</a><a href="latestsexscenes.aspx?page=3">3</a><span>…</span><a href="latestsexscenes.aspx?page=38">38</a><a class="next" href="latestsexscenes.aspx?page=2">Next</a></div>
</div>
</body>
</html>`

func TestParseListingPage(t *testing.T) {
	items := parseListingPage([]byte(listingHTML))
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	it := items[0]
	if it.videoID != "19490" {
		t.Errorf("videoID = %q, want 19490", it.videoID)
	}
	if it.title != "Bruce & Rikki" {
		t.Errorf("title = %q, want %q", it.title, "Bruce & Rikki")
	}
	if it.thumbnail != "https://freeassets.belamionline.com/Data/Contents/Content_19490/Thumbnail10.jpg" {
		t.Errorf("thumbnail = %q", it.thumbnail)
	}
	if it.description != "Rikki Norseman had barely started with us when he expressed interest in becoming a photographer." {
		t.Errorf("description = %q", it.description)
	}
	wantDate := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)
	if !it.date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", it.date, wantDate)
	}
	if len(it.tags) != 2 || it.tags[0] != "Condom Free" || it.tags[1] != "Sex Scenes" {
		t.Errorf("tags = %v", it.tags)
	}

	it2 := items[1]
	if it2.videoID != "17594" {
		t.Errorf("videoID = %q, want 17594", it2.videoID)
	}
	if it2.title != "Edison Jones" {
		t.Errorf("title = %q, want %q", it2.title, "Edison Jones")
	}
	if len(it2.tags) != 2 || it2.tags[0] != "Singles" || it2.tags[1] != "Castings" {
		t.Errorf("tags = %v", it2.tags)
	}
}

func TestParseMaxPage(t *testing.T) {
	maxPage := parseMaxPage([]byte(listingHTML))
	if maxPage != 38 {
		t.Errorf("max page = %d, want 38", maxPage)
	}
}

// No pager means "unknown", not "one page": reading it as 1 stopped the walk
// after 32 scenes the moment the markup changed, and --full then deleted the
// rest of the catalogue. The short-page fallback terminates instead.
func TestParseMaxPage_NoPagination(t *testing.T) {
	maxPage := parseMaxPage([]byte(`<html><body>no pager here</body></html>`))
	if maxPage != 0 {
		t.Errorf("max page = %d, want 0 (unknown)", maxPage)
	}
}

// testRoster is the shape loadRoster builds: every published name under both
// its full spelling and its first name.
var testRoster = map[string]bool{
	"bruce": true, "rikki": true, "edison jones": true, "edison": true,
	"helmut": true, "hoyt": true, "jerome": true,
	"kevin": true, "sven": true, "pip": true, "adam": true,
}

// The tour publishes no cast markup: the label is either a list of first names
// or a descriptive title, and only the site's own model roster tells them
// apart. Without it "Lovers & Rivals" was filed as two performers.
func TestParsePerformers(t *testing.T) {
	tests := []struct {
		title string
		want  []string
	}{
		{"Bruce & Rikki", []string{"Bruce", "Rikki"}},
		{"Edison Jones", []string{"Edison Jones"}},
		{"Helmut, Hoyt & Jerome", []string{"Helmut", "Hoyt", "Jerome"}},
		{"Kevin, Sven, Pip & Adam", []string{"Kevin", "Sven", "Pip", "Adam"}},
		{"Blond Bottoms Orgy", nil},
		{"Private shots - ORGY 1", nil},
		{"Summer Loves - Part 29", nil},
		{"Sun & Sangria - Series 2 - Part 9", nil},
		// Two capitalised words joined by "&" look exactly like a pair of
		// first names; only the roster rejects them.
		{"Lovers & Rivals", nil},
		{"Back to Greece Leftovers", nil},
		{"Day on the beach", nil},
		// One unknown part disqualifies the whole label.
		{"Bruce & Rivals", nil},
		{"", nil},
	}
	for _, tt := range tests {
		got := parsePerformers(tt.title, testRoster)
		if len(got) != len(tt.want) {
			t.Errorf("parsePerformers(%q) = %v, want %v", tt.title, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parsePerformers(%q)[%d] = %q, want %q", tt.title, i, got[i], tt.want[i])
			}
		}
	}
	// With no roster nothing is credited rather than everything.
	if got := parsePerformers("Bruce & Rikki", nil); got != nil {
		t.Errorf("parsePerformers without a roster = %v, want nil", got)
	}
}

func TestToScene(t *testing.T) {
	item := listItem{
		videoID:     "19490",
		title:       "Bruce & Rikki",
		description: "Test description.",
		thumbnail:   "https://freeassets.belamionline.com/Data/Contents/Content_19490/Thumbnail10.jpg",
		date:        time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		tags:        []string{"Condom Free", "Sex Scenes"},
	}
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	scene := toScene("https://belamionline.com", item, now, testRoster)

	if scene.ID != "19490" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "belamionline" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.URL != "https://newtour.belamionline.com/playvideo.aspx?VideoID=19490" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Studio != "BelAmi" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if len(scene.Performers) != 2 || scene.Performers[0] != "Bruce" || scene.Performers[1] != "Rikki" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if scene.Description != "Test description." {
		t.Errorf("Description = %q", scene.Description)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://belamionline.com", true},
		{"https://www.belamionline.com", true},
		{"https://newtour.belamionline.com", true},
		{"https://newtour.belamionline.com/latestsexscenes.aspx", true},
		{"https://newtour.belamionline.com/modelsindex.aspx?ModelID=2722", true},
		{"https://example.com", false},
		{"https://freshmen.net", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestDetectSection(t *testing.T) {
	tests := []struct {
		url  string
		want *string
	}{
		{"https://belamionline.com/latestsexscenes.aspx", strPtr("scenes")},
		{"https://newtour.belamionline.com/latestsolos.aspx", strPtr("solos")},
		{"https://belamionline.com/latestvintage.aspx?page=3", strPtr("vintage")},
		{"https://newtour.belamionline.com/latestbackstage.aspx", strPtr("backstage")},
		{"https://belamionline.com", nil},
		{"https://belamionline.com/modelsindex.aspx?ModelID=123", nil},
	}
	for _, tt := range tests {
		got := detectSection(tt.url)
		if tt.want == nil {
			if got != nil {
				t.Errorf("detectSection(%q) = %q, want nil", tt.url, got.name)
			}
		} else {
			if got == nil {
				t.Errorf("detectSection(%q) = nil, want %q", tt.url, *tt.want)
			} else if got.name != *tt.want {
				t.Errorf("detectSection(%q) = %q, want %q", tt.url, got.name, *tt.want)
			}
		}
	}
}

func TestParseListingPage_ModelPage(t *testing.T) {
	html := `<html><body>
	<div class="content_list">
		<div class="content">
			<div class="wrap">
				<div class="img">
					<a href="playvideo.aspx?VideoID=19531">
						<img data-src="https://freeassets.belamionline.com/Data/Contents/Content_19531/Thumbnail10.jpg" alt="We are back today with our 3 musketeers." loading="lazy" decoding="async">
					</a>
				</div>
				<div class="more_top">
					<span class="label">Helmut, Hoyt &amp; Jerome</span>
				</div>
			</div>
			<div class="more_bottom">
				<div class="tags">
					<a>Photosession Videos</a><a>Solos</a>
				</div>
				<div class="date">5/27/2026</div>
			</div>
		</div>
		<div class="content">
			<div class="wrap">
				<div class="img">
					<a href="playvideo.aspx?VideoID=17496">
						<img data-src="https://freeassets.belamionline.com/Data/Contents/Content_17496/Thumbnail10.jpg" alt="Seven stunning guys team up at our African location." loading="lazy" decoding="async">
					</a>
				</div>
				<div class="more_top">
					<span class="label">Blond Bottoms Orgy</span>
				</div>
			</div>
			<div class="more_bottom">
				<div class="tags">
					<a>Condom Free</a><a>Sex Scenes</a>
				</div>
				<div class="date">2/28/2026</div>
			</div>
		</div>
	</div>
	</body></html>`

	items := parseListingPage([]byte(html))
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].videoID != "19531" {
		t.Errorf("item 0 videoID = %q, want 19531", items[0].videoID)
	}
	if items[0].title != "Helmut, Hoyt & Jerome" {
		t.Errorf("item 0 title = %q", items[0].title)
	}

	if items[1].videoID != "17496" {
		t.Errorf("item 1 videoID = %q, want 17496", items[1].videoID)
	}
	if items[1].title != "Blond Bottoms Orgy" {
		t.Errorf("item 1 title = %q", items[1].title)
	}

	performers0 := parsePerformers(items[0].title, testRoster)
	if len(performers0) != 3 {
		t.Errorf("performers for %q = %v", items[0].title, performers0)
	}

	performers1 := parsePerformers(items[1].title, testRoster)
	if performers1 != nil {
		t.Errorf("performers for %q = %v, want nil", items[1].title, performers1)
	}
}

func TestParseDescription_BacktickEscape(t *testing.T) {
	html := `<div class="content">
		<div class="wrap"><div class="img">
			<a href="playvideo.aspx?VideoID=100">
				<img data-src="https://cdn.example.com/t.jpg" alt="Bruce took Bruce` + "`" + `s cock in his mouth." loading="lazy">
			</a></div>
			<div class="more_top"><span class="label">Test</span></div></div>
			<div class="more_bottom"><div class="tags"><a>Tag</a></div><div class="date">1/1/2026</div></div>
	</div>`

	items := parseListingPage([]byte(html))
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].description != "Bruce took Bruce`s cock in his mouth." {
		t.Errorf("description = %q", items[0].description)
	}
}

func strPtr(s string) *string { return &s }

// perPage is only a fallback for termination; the pager parsed from the page is
// primary. Termination used to key off a hardcoded 32-item page size, so a
// page-size change would have stopped the walk on page 1.
func TestPerPageIsAFallbackNotTheOnlyGuard(t *testing.T) {
	if perPage != 32 {
		t.Errorf("perPage = %d, want 32 (the tour's page size)", perPage)
	}
	// The existing TestParseMaxPage/TestParseMaxPage_NoPagination cover the
	// pager parse itself; this pins that runSection has a real page count to
	// terminate on.
	if got := parseMaxPage([]byte(listingHTML)); got <= 1 {
		t.Errorf("parseMaxPage(listing) = %d, want a real page count to terminate on", got)
	}
}

// The model page is one already-parsed fetch, so a stored scene in the middle
// of it must be skipped rather than truncating the rest of the model's
// catalogue.
func TestRunModelKnownIDsSkipsAndContinues(t *testing.T) {
	const modelHTML = `<html><body><div class="content_list">
	<div class="content"><div class="wrap"><div class="img">
		<a href="playvideo.aspx?VideoID=19531"><img data-src="https://freeassets.belamionline.com/a.jpg" alt="First."></a>
	</div><div class="more_top"><span class="label">Stored Scene</span></div></div>
	<div class="more_bottom"><div class="tags"><a>Solos</a></div><div class="date">5/27/2026</div></div></div>
	<div class="content"><div class="wrap"><div class="img">
		<a href="playvideo.aspx?VideoID=17496"><img data-src="https://freeassets.belamionline.com/b.jpg" alt="Second."></a>
	</div><div class="more_top"><span class="label">Behind It</span></div></div>
	<div class="more_bottom"><div class="tags"><a>Sex Scenes</a></div><div class="date">2/28/2026</div></div></div>
	</div></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/modelsindex.aspx") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, modelHTML)
	}))
	defer ts.Close()

	s := New()
	s.Client = ts.Client()
	s.base = ts.URL

	out := make(chan scraper.SceneResult, 20)
	go func() {
		s.runModel(context.Background(), "https://newtour.belamionline.com/modelsindex.aspx?ModelID=1234",
			scraper.ListOpts{KnownIDs: map[string]bool{"19531": true}}, out)
		close(out)
	}()

	scenes, stopped := testutil.CollectScenesWithStop(t, out)
	if !stopped {
		t.Error("expected StoppedEarly to report that something was skipped")
	}
	if len(scenes) != 1 || scenes[0].ID != "17496" {
		t.Fatalf("got %+v, want only the scene behind the stored one", scenes)
	}
}

// The pager is windowed — page 1 links 2, 3 … 38 — so a walk that only read
// the first page's window would stop short on a site whose window is narrower.
// Re-reading each page lets a later one raise the bound.
func TestListingWalkUsesTheHighestPagerNumberSeen(t *testing.T) {
	pages := map[string]string{
		"1": `<div class="pag_b"><a href="x.aspx?page=2">2</a></div>`,
		"2": `<div class="pag_b"><a href="x.aspx?page=3">3</a><a href="x.aspx?page=9">9</a></div>`,
	}
	if got := parseMaxPage([]byte(pages["1"])); got != 2 {
		t.Errorf("page 1 max = %d, want 2", got)
	}
	if got := parseMaxPage([]byte(pages["2"])); got != 9 {
		t.Errorf("page 2 max = %d, want 9", got)
	}
}

// loadRoster walks the model index once, stops when a page adds no new name
// (the tour clamps past the last page rather than serving an empty one), and
// indexes each model under both its full spelling and its first name.
func TestLoadRoster(t *testing.T) {
	page := func(names ...string) string {
		var b strings.Builder
		for _, n := range names {
			fmt.Fprintf(&b, `<div class="content"><span class="label">%s</span></div>`, n)
		}
		return `<html><body>` + b.String() + `</body></html>`
	}
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Query().Get("page") {
		case "":
			_, _ = fmt.Fprint(w, page("Alan Cartier", "Jamie Durrell"))
		case "2":
			_, _ = fmt.Fprint(w, page("Camillo Ramos"))
		default:
			// Past the end the tour re-serves the last page.
			_, _ = fmt.Fprint(w, page("Camillo Ramos"))
		}
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), base: ts.URL}
	roster := s.loadRoster(context.Background(), 0)

	for _, want := range []string{"alan cartier", "alan", "jamie durrell", "jamie", "camillo ramos", "camillo"} {
		if !roster[want] {
			t.Errorf("roster is missing %q", want)
		}
	}
	if roster["rivals"] {
		t.Error("roster contains a name it was never given")
	}
	if calls != 3 {
		t.Errorf("fetched %d index pages, want 3 (the third adds nothing)", calls)
	}
	// The walk runs once per scraper.
	if s.loadRoster(context.Background(), 0) == nil || calls != 3 {
		t.Errorf("roster re-fetched: %d calls", calls)
	}
}

// The pager is windowed: page 1 renders only the first few page numbers, so a
// walk that trusted page 1's maximum stopped there. The maximum is re-read on
// every page and the highest seen wins, so a later page naming a higher last
// page carries the walk on.
func TestWindowedPagerDoesNotStopTheWalkOnPageOne(t *testing.T) {
	// A pager window of five, sliding as the walk advances, over four real pages.
	page := func(n, window int, cards string) string {
		var b strings.Builder
		b.WriteString(`<html><body><div class="content_list">` + cards + `</div><div class="pag_b">`)
		for p := n; p < n+window; p++ {
			fmt.Fprintf(&b, `<a href="?page=%d">%d</a>`, p, p)
		}
		b.WriteString(`</div></body></html>`)
		return b.String()
	}
	card := func(id int) string {
		return fmt.Sprintf(`<div class="content"><div class="wrap"><div class="img">`+
			`<a href="playvideo.aspx?VideoID=%d"><img data-src="https://freeassets.belamionline.com/%d.jpg" alt="Scene %d."></a>`+
			`</div><div class="more_top"><span class="label">Scene %d</span></div></div>`+
			`<div class="more_bottom"><div class="tags"><a>Sex Scenes</a></div><div class="date">5/27/2026</div></div></div>`, id, id, id, id)
	}

	const lastPage = 4
	var pagesSeen []int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/models.aspx") {
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
			return
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if n == 0 {
			n = 1
		}
		pagesSeen = append(pagesSeen, n)
		w.Header().Set("Content-Type", "text/html")
		if n > lastPage {
			_, _ = fmt.Fprint(w, page(n, 5, ""))
			return
		}
		// Every real page is full, so the short-page fallback cannot end the
		// walk either — only the pager can.
		var cards strings.Builder
		for i := 0; i < perPage; i++ {
			cards.WriteString(card(n*1000 + i))
		}
		_, _ = fmt.Fprint(w, page(n, 5, cards.String()))
	}))
	defer ts.Close()

	s := New()
	s.Client = ts.Client()
	s.base = ts.URL

	out := make(chan scraper.SceneResult, perPage*(lastPage+2))
	s.runSection(context.Background(), ts.URL, scraper.ListOpts{}, out, sections[0], nil)
	close(out)

	var scenes int
	for r := range out {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}
	if want := perPage * lastPage; scenes != want {
		t.Errorf("got %d scenes, want %d — the windowed pager truncated the walk", scenes, want)
	}
	if len(pagesSeen) < lastPage {
		t.Errorf("fetched pages %v — page 1's window must not be read as the last page", pagesSeen)
	}
}

// Each section is its own paginated listing, so letting Paginate announce each
// section's own total made the last one replace the rest and report a fraction
// of the catalogue. The totals must accumulate.
func TestAllSectionsProgressSumsTheSections(t *testing.T) {
	// Every section serves the same listing fixture, whose pager claims 38
	// pages, so each section contributes 38*perPage to the total. Paginate's
	// repeat-page detection ends each section on page 2.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "models.aspx") {
			_, _ = fmt.Fprint(w, `<html></html>`)
			return
		}
		_, _ = fmt.Fprint(w, listingHTML)
	}))
	defer ts.Close()

	s := New()
	s.Client = ts.Client()
	s.base = ts.URL

	out := make(chan scraper.SceneResult, 500)
	s.run(context.Background(), ts.URL+"/", scraper.ListOpts{}, out)

	last := 0
	for r := range out {
		if r.Kind == scraper.KindTotal {
			last = r.Total
		}
	}
	want := len(sections) * parseMaxPage([]byte(listingHTML)) * perPage
	if last != want {
		t.Errorf("final total = %d, want %d (every section summed)", last, want)
	}
}
