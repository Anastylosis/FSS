package groobyutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

var _ scraper.StudioScraper = (*Scraper)(nil)

var testCfg = SiteConfig{
	SiteID:     "testsite",
	Domain:     "testsite.com",
	StudioName: "Test Studio",
	TourPrefix: "/tour",
}

const testCardHTML = `<div class="sexyvideo">
	<div class="setbg">
		<a href="https://www.testsite.com/tour/trailers/my-scene.html">
			<img id="set-target-12345" class="mainThumb thumbs stdimage" src="/content/thumbs/12345.jpg">
		</a>
	</div>
	<h4><a href="https://www.testsite.com/tour/trailers/my-scene.html" title="Amazing Scene Title">Amazing Scene Title</a></h4>
	<div class="modelname"><a href="/tour/models/jane-doe.html"><span class="text-center">Jane Doe</span></a></div>
	<div class="modelname"><a href="/tour/models/john-smith.html"><span class="text-center">John Smith</span></a></div>
	<p class="photodesc">A great scene description here.</p>
	<p class="dateadded"><span><i class='fas fa-video'></i> <div class="duration-div">16:56&nbsp;HD&nbsp;Video</div></span> <i class='far fa-calendar' style='margin-left:10px;'></i> 8th May 2026</p>
</div>`

const testCardMinimal = `<div class="sexyvideo">
	<div class="setbg">
		<a href="/tour/trailers/solo.html">
			<img id="set-target-999" class="mainThumb thumbs stdimage" src="/content/thumbs/999.jpg">
		</a>
	</div>
	<h4><a href="/tour/trailers/solo.html" title="Solo Scene">Solo Scene</a></h4>
	<div class="modelname"><a href="/tour/models/performer.html"><span class="text-center">Solo Performer</span></a></div>
	<p class="dateadded"><span><i class='fas fa-video'></i> <div class="duration-div">1:05:30&nbsp;HD&nbsp;Video</div></span> <i class='far fa-calendar' style='margin-left:10px;'></i> 23rd January 2025</p>
</div>`

func TestParseListingPage(t *testing.T) {
	body := []byte(testCardHTML + testCardMinimal)
	scenes := parseListingPage(body)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s := scenes[0]
	if s.id != "12345" {
		t.Errorf("id = %q, want 12345", s.id)
	}
	if s.title != "Amazing Scene Title" {
		t.Errorf("title = %q, want %q", s.title, "Amazing Scene Title")
	}
	if s.url != "https://www.testsite.com/tour/trailers/my-scene.html" {
		t.Errorf("url = %q", s.url)
	}
	if s.thumb != "/content/thumbs/12345.jpg" {
		t.Errorf("thumb = %q", s.thumb)
	}
	if s.date.Format("2006-01-02") != "2026-05-08" {
		t.Errorf("date = %v, want 2026-05-08", s.date)
	}
	if s.duration != 1016 {
		t.Errorf("duration = %d, want 1016", s.duration)
	}
	if len(s.performers) != 2 || s.performers[0] != "Jane Doe" || s.performers[1] != "John Smith" {
		t.Errorf("performers = %v, want [Jane Doe John Smith]", s.performers)
	}
	if s.description != "A great scene description here." {
		t.Errorf("description = %q", s.description)
	}

	s2 := scenes[1]
	if s2.id != "999" {
		t.Errorf("id = %q, want 999", s2.id)
	}
	if s2.title != "Solo Scene" {
		t.Errorf("title = %q, want Solo Scene", s2.title)
	}
	if s2.duration != 3930 {
		t.Errorf("duration = %d, want 3930 (1:05:30)", s2.duration)
	}
	if len(s2.performers) != 1 || s2.performers[0] != "Solo Performer" {
		t.Errorf("performers = %v, want [Solo Performer]", s2.performers)
	}
	if s2.date.Format("2006-01-02") != "2025-01-23" {
		t.Errorf("date = %v, want 2025-01-23", s2.date)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"16:56", 1016},
		{"5:00", 300},
		{"0:30", 30},
		{"1:05:30", 3930},
		{"2:00:00", 7200},
	}
	for _, tt := range tests {
		if got := parseutil.ParseDurationColon(tt.in); got != tt.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestParseGroobyDate(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"8th May 2026", "2026-05-08"},
		{"1st January 2025", "2025-01-01"},
		{"2nd February 2024", "2024-02-02"},
		{"3rd March 2023", "2023-03-03"},
		{"23rd January 2025", "2025-01-23"},
		{"11th November 2020", "2020-11-11"},
		{"21st December 2019", "2019-12-21"},
	}
	for _, tt := range tests {
		got := parseGroobyDate(tt.in)
		if got.Format("2006-01-02") != tt.want {
			t.Errorf("parseGroobyDate(%q) = %v, want %s", tt.in, got, tt.want)
		}
	}
}

func TestEstimateTotal(t *testing.T) {
	body := []byte(`<a href="movies_5_d.html">5</a><a href="movies_20_d.html">20</a>`)
	if got := estimateTotal(body, 12); got != 240 {
		t.Errorf("estimateTotal = %d, want 240", got)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{
		SiteID:     "test",
		Domain:     "black-tgirls.com",
		StudioName: "Black TGirls",
		TourPrefix: "/tour",
	})

	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.black-tgirls.com/tour/", true},
		{"https://black-tgirls.com/tour/", true},
		{"http://www.black-tgirls.com/", true},
		{"https://other-site.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestMatchesURLAltDomains(t *testing.T) {
	s := New(SiteConfig{
		SiteID:     "test",
		Domain:     "groobyvr.com",
		StudioName: "Grooby VR",
		TourPrefix: "/tour",
		AltDomains: []string{"justvr.xxx"},
	})

	if !s.MatchesURL("https://www.justvr.xxx/tour/") {
		t.Error("expected alt domain justvr.xxx to match")
	}
	if !s.MatchesURL("https://www.groobyvr.com/tour/") {
		t.Error("expected primary domain to match")
	}
}

const cardTpl = `<div class="sexyvideo">
	<div class="setbg">
		<a href="/tour/trailers/scene-%d.html">
			<img id="set-target-%d" class="mainThumb thumbs stdimage" src="/content/thumbs/%d.jpg">
		</a>
	</div>
	<h4><a href="/tour/trailers/scene-%d.html" title="Scene %d">Scene %d</a></h4>
	<div class="modelname"><a href="/tour/models/test.html"><span class="text-center">Test</span></a></div>
	<p class="dateadded"><span><i class='fas fa-video'></i> <div class="duration-div">10:00&nbsp;HD&nbsp;Video</div></span> <i class='far fa-calendar' style='margin-left:10px;'></i> 1st January 2025</p>
</div>`

func buildTestPage(ids []int, maxPage int) []byte {
	var sb strings.Builder
	pager := ""
	for p := 2; p <= maxPage; p++ {
		pager += fmt.Sprintf(`<a href="movies_%d_d.html">%d</a>`, p, p)
	}
	sb.WriteString(pager)
	for _, id := range ids {
		fmt.Fprintf(&sb, cardTpl, id, id, id, id, id, id)
	}
	return []byte(sb.String())
}

var testPageNumRe = regexp.MustCompile(`_(\d+)_d\.html`)

func extractPageNum(path string) int {
	if m := testPageNumRe.FindStringSubmatch(path); m != nil {
		p, _ := strconv.Atoi(m[1])
		return p
	}
	return 1
}

func newTestServer(pages [][]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")

		switch {
		case strings.Contains(r.URL.Path, "/models/"):
			_, _ = w.Write(buildTestPage(pages[0], 1))
		default:
			pageNum := extractPageNum(r.URL.Path)
			idx := pageNum - 1
			if idx >= 0 && idx < len(pages) {
				_, _ = w.Write(buildTestPage(pages[idx], len(pages)))
			} else {
				_, _ = fmt.Fprint(w, `<div>empty</div>`)
			}
		}
	}))
}

func testScraper(ts *httptest.Server) *Scraper {
	s := New(testCfg)
	s.base = ts.URL
	return s
}

func TestRun(t *testing.T) {
	ts := newTestServer([][]int{{100, 200}})
	defer ts.Close()

	s := testScraper(ts)

	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/", scraper.ListOpts{
		Delay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2", len(got))
	}
	if got[0].Title != "Scene 100" {
		t.Errorf("title = %q, want Scene 100", got[0].Title)
	}
	if got[0].SiteID != "testsite" {
		t.Errorf("siteID = %q, want testsite", got[0].SiteID)
	}
	if got[0].Studio != "Test Studio" {
		t.Errorf("studio = %q, want Test Studio", got[0].Studio)
	}
}

func TestKnownIDs(t *testing.T) {
	ts := newTestServer([][]int{{1, 2, 3, 4}})
	defer ts.Close()

	s := testScraper(ts)

	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/", scraper.ListOpts{
		KnownIDs: map[string]bool{"3": true},
		Delay:    time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, stopped := testutil.CollectScenesWithStop(t, ch)
	if len(got) != 3 {
		t.Fatalf("got %d scenes, want 3 — the page is finished before the stop", len(got))
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
}

func TestPagination(t *testing.T) {
	page1 := []int{10, 20, 30}
	page2 := []int{40, 50}

	ts := newTestServer([][]int{page1, page2})
	defer ts.Close()

	s := testScraper(ts)

	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/", scraper.ListOpts{
		Delay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 5 {
		t.Fatalf("got %d scenes, want 5", len(got))
	}
}

func TestModelPage(t *testing.T) {
	ts := newTestServer([][]int{{10, 20, 30}})
	defer ts.Close()

	s := testScraper(ts)

	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/models/TestModel.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 3 {
		t.Fatalf("got %d scenes, want 3", len(got))
	}
}

func TestToSceneRelativeURLs(t *testing.T) {
	item := sceneItem{
		id:    "42",
		title: "Test",
		url:   "/tour/trailers/test.html",
		thumb: "/content/thumbs/42.jpg",
	}
	scene := item.toScene("testid", "Test Studio", "https://www.example.com", time.Now())
	if scene.URL != "https://www.example.com/tour/trailers/test.html" {
		t.Errorf("URL = %q, want absolute", scene.URL)
	}
	if scene.Thumbnail != "https://www.example.com/content/thumbs/42.jpg" {
		t.Errorf("Thumbnail = %q, want absolute", scene.Thumbnail)
	}
}

func TestToSceneAbsoluteURLs(t *testing.T) {
	item := sceneItem{
		id:    "42",
		title: "Test",
		url:   "https://www.example.com/tour/trailers/test.html",
		thumb: "https://cdn.example.com/thumbs/42.jpg",
	}
	scene := item.toScene("testid", "Test Studio", "https://www.example.com", time.Now())
	if scene.URL != "https://www.example.com/tour/trailers/test.html" {
		t.Errorf("URL = %q, should not be modified", scene.URL)
	}
	if scene.Thumbnail != "https://cdn.example.com/thumbs/42.jpg" {
		t.Errorf("Thumbnail = %q, should not be modified", scene.Thumbnail)
	}
}

// TestParseListingSkipsComingSoon covers unreleased scenes. The tour renders
// them as a card with a countdown and no trailer link, so they carry a
// set-target id but no title and no URL — emitting them would produce scenes
// with empty required fields.
func TestParseListingSkipsComingSoon(t *testing.T) {
	body := []byte(`
<div class="sexyvideo">
  <div class="videoblock">
    <div class="modelname"><a href="/tour/models/Kawaii-Fiona.html"><span class="text-center">Kawaii Fiona</span></a></div>
    <div class="epochtime">
      <img id="set-target-11544" alt="It&#039;s Been A While ..." class="mainThumb thumbs stdimage" src="/tour/content//contentthumbs/86/95/268695-1x.jpg" />
    </div>
    <div class="comingsoon" style="text-align: center;">Coming Soon!<br>
      <div class="countdown" data-end="1784714400"></div>
    </div>
  </div>
</div>
<div class="sexyvideo">
  <div class="videoblock">
    <span><i class='fas fa-video'></i> <div style='color: #ff0; display:inline'>17:02&nbsp;HD&nbsp;Video</div></span>
    <div class="modelname"><a href="/tour/models/Someone.html"><span class="text-center">Someone</span></a></div>
    <img id="set-target-11545" class="mainThumb thumbs stdimage" src="/tour/content//x-1x.jpg" />
    <h4><a href="https://www.groobygirls.com/tour/trailers/Sweet-Look-Serious-Body.html" title="Sweet Look. Serious Body">x</a></h4>
  </div>
</div>`)

	items := parseListingPage(body)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (the Coming Soon card must be skipped)", len(items))
	}
	if items[0].id != "11545" {
		t.Errorf("id = %q, want 11545 (the released scene)", items[0].id)
	}
	if items[0].title == "" || items[0].url == "" {
		t.Errorf("released scene lost its title/url: %+v", items[0])
	}
}

// N8 / NL4: a `comingsoon` element in the page footer must not drop the last real card.
//
// Card blocks used to run to the end of the page for the final card, so the last
// block swallowed the footer and sidebar. Any Coming-Soon promo outside the grid then
// matched, and the last genuine scene of *every* listing page was skipped — a steady
// 1-in-N loss with no error, which `--full` converts into a hard delete of those scenes
// and their price history.
func TestParseListingKeepsLastCardDespiteFooterComingSoon(t *testing.T) {
	body := []byte(`
<div class="sexyvideo">
  <div class="videoblock">
    <div class="modelname"><a href="/tour/models/A.html"><span class="text-center">Model A</span></a></div>
    <img id="set-target-11111" class="mainThumb thumbs stdimage" src="/tour/content/a-1x.jpg" />
    <h4><a href="https://www.groobygirls.com/tour/trailers/First-Scene.html" title="First Scene">x</a></h4>
  </div>
</div>
<div class="sexyvideo">
  <div class="videoblock">
    <div class="modelname"><a href="/tour/models/B.html"><span class="text-center">Model B</span></a></div>
    <img id="set-target-22222" class="mainThumb thumbs stdimage" src="/tour/content/b-1x.jpg" />
    <h4><a href="https://www.groobygirls.com/tour/trailers/Last-Scene.html" title="Last Scene">x</a></h4>
  </div>
</div>
<footer>
  <div class="sidebar">
    <div class="comingsoon" style="text-align: center;">Coming Soon!<br>
      <div class="countdown" data-end="1784714400"></div>
    </div>
  </div>
</footer>`)

	items := parseListingPage(body)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 — the footer's comingsoon element must not "+
			"reach the last card's block", len(items))
	}
	if items[1].id != "22222" {
		t.Errorf("last item id = %q, want 22222", items[1].id)
	}
	if items[1].url == "" || items[1].title == "" {
		t.Errorf("last item is incomplete: %+v", items[1])
	}
}

// The genuine case must still be skipped: a Coming-Soon card that is itself the last
// card on the page.
func TestParseListingStillSkipsTrailingComingSoonCard(t *testing.T) {
	body := []byte(`
<div class="sexyvideo">
  <div class="videoblock">
    <div class="modelname"><a href="/tour/models/A.html"><span class="text-center">Model A</span></a></div>
    <img id="set-target-11111" class="mainThumb thumbs stdimage" src="/tour/content/a-1x.jpg" />
    <h4><a href="https://www.groobygirls.com/tour/trailers/Real.html" title="Real">x</a></h4>
  </div>
</div>
<div class="sexyvideo">
  <div class="videoblock">
    <div class="modelname"><a href="/tour/models/B.html"><span class="text-center">Model B</span></a></div>
    <div class="epochtime">
      <img id="set-target-22222" class="mainThumb thumbs stdimage" src="/tour/content/b-1x.jpg" />
    </div>
    <div class="comingsoon" style="text-align: center;">Coming Soon!<br>
      <div class="countdown" data-end="1784714400"></div>
    </div>
  </div>
</div>`)

	items := parseListingPage(body)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 — a trailing Coming Soon *card* must still be skipped", len(items))
	}
	if items[0].id != "11111" {
		t.Errorf("kept item id = %q, want 11111", items[0].id)
	}
}

// ---- the newer `sexyvideo_outer` card ----

// outerCardHTML reproduces the newer template, where the credit block, the
// runtime and the date all sit OUTSIDE the inner `sexyvideo` div. Chunking on
// the inner div alone silently dropped all three on 26 of the 42 registered
// sites — 488 of 488 uk-tgirls scenes had no performer, no date and no
// duration. Note the details that differ from the older card: a site-logo
// <img> before the model link, the name as anchor text rather than in a
// <span>, `&nbsp;` between the video icon and the runtime, `fa-calendar-check`
// rather than `fa-calendar`, and scheme-relative hrefs.
const outerCardHTML = `<div class="sexyvideo_outer">
	<div class="modelnamecontainer">
		<div class="modelname">
		<img class="logo_x" src="https://www.testsite.com/tour/custom_assets/images/logo_x.png">
<a href="//www.testsite.com/models/AdaStone.html">Ada Stone</a>
		</div>
	</div>
	<div class="sexyvideo">
		<div class="videoblock"><div class="videohere">
			<div class="video_stats">
			<i class='fas fa-video'></i>&nbsp;&nbsp;<div style='display:inline'>20:27&nbsp;HD Video</div> &amp; 50&nbsp;Photos			</div>
			<a href="//www.testsite.com/trailers/ada-stone-scene.html" title="A Newer Card">
			<img id="set-target-1341" alt="A Newer Card" class="mainThumb thumbs stdimage" src="/content//contentthumbs/45/47/24547-2x.jpg" />
			</a>
		</div></div>
		<h4><a href="//www.testsite.com/trailers/ada-stone-scene.html" title="A Newer Card">A Newer Card</a></h4>
		<p class="photodesc">A description on the newer template.</p>
		<div class="dateadded" STYLE="DISPLAY: NONE"><i class='far fa-calendar-check' style="color: #295777"></i> 17th Aug 2026</div>
		<div class="rating"><i class="fa fa-trophy"></i> Rating: 4.64</div>
	</div>
</div>`

func TestParseListingOuterCardKeepsPerformerDateAndDuration(t *testing.T) {
	items := parseListingPage([]byte(`<html><body><div class="videos clear">` + outerCardHTML + `</div></body></html>`))
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	got := items[0]

	if got.id != "1341" {
		t.Errorf("id = %q", got.id)
	}
	if got.title != "A Newer Card" {
		t.Errorf("title = %q", got.title)
	}
	// The whole point: these three live outside the inner card.
	if len(got.performers) != 1 || got.performers[0] != "Ada Stone" {
		t.Errorf("performers = %v, want [Ada Stone]", got.performers)
	}
	if got.duration != 1227 {
		t.Errorf("duration = %d, want 1227 (20:27)", got.duration)
	}
	if got.date.Format("2006-01-02") != "2026-08-17" {
		t.Errorf("date = %v, want 2026-08-17", got.date)
	}
	if got.description != "A description on the newer template." {
		t.Errorf("description = %q", got.description)
	}
}

// A page carrying only the older card shape must parse exactly as before: the
// outer wrapper is preferred, not required.
func TestParseListingStillHandlesTheOlderCard(t *testing.T) {
	items := parseListingPage([]byte(`<html><body>` + testCardHTML + testCardMinimal + `</body></html>`))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if len(items[0].performers) != 2 {
		t.Errorf("performers = %v, want both credits", items[0].performers)
	}
	if items[0].duration != 1016 {
		t.Errorf("duration = %d, want 1016 (16:56)", items[0].duration)
	}
	if items[0].date.Format("2006-01-02") != "2026-05-08" {
		t.Errorf("date = %v", items[0].date)
	}
}

// Each outer card must claim only its own credit. The blocks are siblings, so a
// chunking mistake pairs a scene with the next card's performer.
func TestOuterCardsDoNotBorrowEachOthersCredits(t *testing.T) {
	second := strings.NewReplacer(
		"AdaStone", "MaraVance",
		"Ada Stone", "Mara Vance",
		"set-target-1341", "set-target-1342",
		"ada-stone-scene", "mara-vance-scene",
		"A Newer Card", "Another Card",
	).Replace(outerCardHTML)

	items := parseListingPage([]byte(`<html><body>` + outerCardHTML + second + `</body></html>`))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	want := map[string]string{"1341": "Ada Stone", "1342": "Mara Vance"}
	for _, it := range items {
		if len(it.performers) != 1 {
			t.Fatalf("scene %s has performers %v, want exactly one", it.id, it.performers)
		}
		if it.performers[0] != want[it.id] {
			t.Errorf("scene %s credited %q, want %q", it.id, it.performers[0], want[it.id])
		}
	}
}

// Scheme-relative hrefs are what the newer template writes. Joined as if they
// were rooted paths they became `https://host//host/trailers/x.html`.
func TestToSceneSchemeRelativeURLs(t *testing.T) {
	item := sceneItem{
		id:    "42",
		title: "Test",
		url:   "//tour.example.com/trailers/test.html",
		thumb: "//cdn.example.com/thumbs/42.jpg",
	}
	scene := item.toScene("testid", "Test Studio", "https://tour.example.com", time.Now())
	if scene.URL != "https://tour.example.com/trailers/test.html" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Thumbnail != "https://cdn.example.com/thumbs/42.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
}

// `www.` belongs in front of a bare apex only. Prefixing a host that already
// names a subdomain produced `www.tour.transerotica.com`, which serves a
// certificate for another host and fails the handshake outright.
func TestHostForOnlyPrefixesAnApex(t *testing.T) {
	cases := map[string]string{
		"black-tgirls.com":      "www.black-tgirls.com",
		"tgirls.porn":           "www.tgirls.porn",
		"braziltgirls.xxx":      "www.braziltgirls.xxx",
		"tour.transerotica.com": "tour.transerotica.com",
		"a.b.example.co.uk":     "a.b.example.co.uk",
	}
	for in, want := range cases {
		if got := hostFor(in); got != want {
			t.Errorf("hostFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// The performer sub-tours link every card through the NATS affiliate redirect,
// which is a billing URL carrying a tracking code rather than the scene's own
// address. The tour serves the same scene at its own `/trailers/{slug}.html`.
func TestToSceneRewritesTheAffiliateRedirect(t *testing.T) {
	item := sceneItem{
		id:    "2134",
		title: "Test",
		url:   "https://join.transerotica.com/track/MC4wLjEwOS4xODAuMC4wLjAuMC4w/trailers/Some-Scene.html",
	}
	scene := item.toScene("cherrymavrik", "Cherry Mavrik", "https://www.cherrymavrik.com", time.Now())
	if want := "https://www.cherrymavrik.com/trailers/Some-Scene.html"; scene.URL != want {
		t.Errorf("URL = %q, want %q", scene.URL, want)
	}
}

// Only the redirect shape is rewritten. Any other absolute URL — a CDN
// thumbnail, a scene already on its own host — is left exactly as published.
func TestToSceneLeavesOtherAbsoluteURLsAlone(t *testing.T) {
	for _, u := range []string{
		"https://tour.example.com/trailers/Some-Scene.html",
		"https://join.example.com/signup/signup.php?nats=abc",
		"https://cdn.example.com/thumbs/1.jpg",
	} {
		item := sceneItem{id: "1", title: "T", url: u}
		if got := item.toScene("x", "X", "https://www.example.com", time.Now()).URL; got != u {
			t.Errorf("URL %q was rewritten to %q", u, got)
		}
	}
}
