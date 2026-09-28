package realspankingsutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

const fixtureRSI = `<html><body>
<table><tr>
<td align="right"><a href="updates.php?v=cGFnZT0x" style="font-size:13px;">Next Page</a></td>
</tr></table>
<tr bgcolor="#ECDFAA">
<td align="center" class="top_border">
<table border="0"><tr><td><a href="update_images.php?v=aWQ9MTI1NjQ="><img src="updates/12564_009.jpg" border="0" class="image_border"></a></td></tr></table>
<a href="update_images.php?v=aWQ9MTI1NjQ=">Two Girl Spanking (Part 2) <br>
Thu. May 01, 2026</a>
</td>
<td align="center" class="top_border">
<table border="0"><tr><td><a href="update_images.php?v=aWQ9MTM5ODY="><img src="updates/13986_029.jpg" border="0" class="image_border"></a></td></tr></table>
<a href="update_images.php?v=aWQ9MTM5ODY=">Paddled for Being Late <br>
Wed. Apr 30, 2026</a>
</td>
</tr>
</body></html>`

const fixtureSC = `<html><body>
<div>Page: 1 of 2</div>
<div id="resultScenes">
<div class="searchResultsFirst">
<div class="resultSceneImages">
<img src="updates/10582_001.jpg" alt="Scene 1" />
</div>
<div class="resultSceneData">
<div class="episodeTitle">Jenna: Bedroom Punishment (Part 1)</div>
<div class="episodeDescription">A sound belting for Jenna.</div>
<div class="episodeUpdate">Updated: Wed. May. 06, 2026</div>
</div>
<div style="clear: both;"></div>
</div>
<div class="searchResultsMiddle">
<div class="resultSceneImages">
<img src="updates/10580_003.jpg" alt="Scene 2" />
</div>
<div class="resultSceneData">
<div class="episodeTitle">Bailey &amp; Chloe: Double Trouble</div>
<div class="episodeDescription">Both girls get spanked hard.</div>
<div class="episodeUpdate">Updated: Mon. May. 04, 2026</div>
</div>
<div style="clear: both;"></div>
</div>
</div>
</body></html>`

const fixtureSTB = `<html><body>
<td class="mainText" style="padding-left: 5px; vertical-align: top;">
<div style="text-align: right; float: left; width: 125px;"><a href="viewImage.php?v=dXBkYXRlSWQ9MTc5Nw=="><img src="updates/1797_029.jpg" border="0"></a></div>
<div style="float: left; width: 220px; margin-left: 5px;">
<div style="margin-top: 5px;">Brandi: Spanked for Her Sassy Mouth (Part 2)</div>
<div style="margin-top: 5px; font-style: italic; font-weight: normal;">Dec. 26, 2025</div>
<div style="margin-top: 5px;">90 Pictures</div>
</div>
<div style="clear: both;"></div>
</td>
<td class="mainText" style="padding-left: 5px; vertical-align: top;">
<div style="text-align: right; float: left; width: 125px;"><a href="viewImage.php?v=dXBkYXRlSWQ9MTc5Ng=="><img src="updates/1796_011.jpg" border="0"></a></div>
<div style="float: left; width: 220px; margin-left: 5px;">
<div style="margin-top: 5px;">Brandi: Hand Spanking in the Kitchen</div>
<div style="margin-top: 5px; font-style: italic; font-weight: normal;">Dec. 19, 2025</div>
<div style="margin-top: 5px;">85 Pictures</div>
</div>
<div style="clear: both;"></div>
</td>
</body></html>`

const fixtureSTJ = `<html><body>
<table>
<tr class="menuText" valign="top">
<td width="80" align="left"><a href="image_view.php?v=aWQ9ODIyNDcz"><img src="updates/1801_007.jpg" width="60" height="60" border="0"></a></td>
<td width="330" align="left">
<table width="330"><tr>
<td colspan="3" class="menuText">
<b><span style="color: #000000;">Jessica: Caned For Not Wearing a Bra</span></b><br>
<div style="font-size: 10px;">Updated on 05/05/26</div>
</td></tr></table>
</td>
</tr>
<tr class="menuText" valign="top">
<td width="80" align="left"><a href="image_view.php?v=aWQ9NTA5ODE4"><img src="updates/10392_004.jpg" width="60" height="60" border="0"></a></td>
<td width="330" align="left">
<table width="330"><tr>
<td colspan="3" class="menuText">
<b><span style="color: #000000;">Jessica &amp; Brandi: OTK Together</span></b><br>
<div style="font-size: 10px;">Updated on 04/28/26</div>
</td></tr></table>
</td>
</tr>
</table>
</body></html>`

const fixtureBailey = `<html><body>
<table>
<tr><td style="vertical-align: top; padding: 10px;">
<a href="freeImage.php?v=aWQ9OTAyNg==" style="display: block;" target="_blank"><img src="updates/9026_030.jpg" border="0" alt="Bailey's OTK Caning" /></a>
<div class="mainText10b">Bailey's OTK Caning</div>
<div class="mainText12Bb">Fri. Apr. 24, 2026</div>
</td>
<td style="vertical-align: top; padding: 10px;">
<a href="freeImage.php?v=aWQ9OTAyNQ==" style="display: block;" target="_blank"><img src="updates/9025_034.jpg" border="0" alt="Bailey Gets the Belt" /></a>
<div class="mainText10b">Bailey Gets the Belt</div>
<div class="mainText12Bb">Thu. Apr. 17, 2026</div>
</td></tr>
</table>
</body></html>`

func TestParseRSI(t *testing.T) {
	items := parseRSI([]byte(fixtureRSI), "https://www.realspankingsinstitute.com")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].id != "12564" {
		t.Errorf("item[0].id = %q, want %q", items[0].id, "12564")
	}
	if items[0].title != "Two Girl Spanking (Part 2)" {
		t.Errorf("item[0].title = %q", items[0].title)
	}
	if items[0].date.IsZero() {
		t.Error("item[0].date is zero")
	}
	if items[1].id != "13986" {
		t.Errorf("item[1].id = %q, want %q", items[1].id, "13986")
	}
}

func TestParseSpankedCoeds(t *testing.T) {
	items := parseSpankedCoeds([]byte(fixtureSC), "https://spankedcoeds.com")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].id != "10582" {
		t.Errorf("item[0].id = %q, want %q", items[0].id, "10582")
	}
	if items[0].title != "Jenna: Bedroom Punishment (Part 1)" {
		t.Errorf("item[0].title = %q", items[0].title)
	}
	if items[0].description != "A sound belting for Jenna." {
		t.Errorf("item[0].description = %q", items[0].description)
	}
	if items[1].title != "Bailey & Chloe: Double Trouble" {
		t.Errorf("item[1].title = %q, want %q", items[1].title, "Bailey & Chloe: Double Trouble")
	}
}

func TestParseSTB(t *testing.T) {
	items := parseSTB([]byte(fixtureSTB), "https://spankingteenbrandi.com")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].id != "1797" {
		t.Errorf("item[0].id = %q, want %q", items[0].id, "1797")
	}
	if items[0].title != "Brandi: Spanked for Her Sassy Mouth (Part 2)" {
		t.Errorf("item[0].title = %q", items[0].title)
	}
	if items[0].date.Year() != 2025 || items[0].date.Month() != 12 {
		t.Errorf("item[0].date = %v", items[0].date)
	}
}

func TestParseSTJ(t *testing.T) {
	items := parseSTJ([]byte(fixtureSTJ), "https://spankingteenjessica.com")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].id != "1801" {
		t.Errorf("item[0].id = %q, want %q", items[0].id, "1801")
	}
	if items[0].title != "Jessica: Caned For Not Wearing a Bra" {
		t.Errorf("item[0].title = %q", items[0].title)
	}
	if items[1].title != "Jessica & Brandi: OTK Together" {
		t.Errorf("item[1].title = %q", items[1].title)
	}
}

func TestParseBailey(t *testing.T) {
	items := parseBailey([]byte(fixtureBailey), "https://spankingbailey.com")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].id != "9026" {
		t.Errorf("item[0].id = %q, want %q", items[0].id, "9026")
	}
	if items[0].title != "Bailey's OTK Caning" {
		t.Errorf("item[0].title = %q", items[0].title)
	}
	if items[0].date.IsZero() {
		t.Error("item[0].date is zero")
	}
}

func TestListScenesRSI(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/updates.php":
			v := r.URL.Query().Get("v")
			if v == encodeBase64("page=0") || v == "" {
				_, _ = fmt.Fprint(w, fixtureRSI)
			} else {
				_, _ = fmt.Fprint(w, `<html><body></body></html>`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{
		client: ts.Client(),
		base:   ts.URL,
		cfg:    SiteConfig{SiteID: "test-rsi", Domain: "test.com", StudioName: "Test RSI", Type: TypeRSI},
	}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var sceneCount int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			sceneCount++
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if sceneCount != 2 {
		t.Errorf("got %d scenes, want 2", sceneCount)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, fixtureRSI)
	}))
	defer ts.Close()

	s := &Scraper{
		client: ts.Client(),
		base:   ts.URL,
		cfg:    SiteConfig{SiteID: "test-rsi", Domain: "test.com", StudioName: "Test RSI", Type: TypeRSI},
	}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"12564": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotStoppedEarly bool
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			t.Error("should not have received a scene")
		case scraper.KindStoppedEarly:
			gotStoppedEarly = true
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if !gotStoppedEarly {
		t.Error("expected StoppedEarly")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{
		SiteID: "realspankingsinstitute", Domain: "www.realspankingsinstitute.com",
		StudioName: "Real Spankings Institute", Type: TypeRSI,
	})

	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.realspankingsinstitute.com/updates.php", true},
		{"https://realspankingsinstitute.com/updates.php", true},
		{"https://other-site.com/", false},
	}
	for _, tc := range tests {
		got := s.MatchesURL(tc.url)
		if got != tc.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestEncodeBase64(t *testing.T) {
	got := encodeBase64("page=0")
	want := "cGFnZT0w"
	if got != want {
		t.Errorf("encodeBase64(%q) = %q, want %q", "page=0", got, want)
	}
}

// Go's `02` verb rejects an unpadded day outright, so a site that renders
// "Jan. 5, 2026" alongside "Jan. 15, 2026" silently lost a third of its dates.
// The unpadded verb accepts both spellings.
func TestDateLayoutsAcceptUnpaddedDays(t *testing.T) {
	cases := []struct{ layout, in string }{
		{"Mon. Jan 2, 2006", "Fri. Jan 2, 2026"},
		{"Mon. Jan 2, 2006", "Fri. Jan 02, 2026"},
		{"Mon. Jan. 2, 2006", "Fri. Jan. 2, 2026"},
		{"Mon. Jan. 2, 2006", "Fri. Jan. 02, 2026"},
		{"Jan. 2, 2006", "Jan. 2, 2026"},
		{"Jan. 2, 2006", "Jan. 02, 2026"},
	}
	for _, c := range cases {
		if _, err := time.Parse(c.layout, c.in); err != nil {
			t.Errorf("time.Parse(%q, %q): %v", c.layout, c.in, err)
		}
	}
}

// These sites publish no per-scene page, so every scene used to be stored with
// the site root as its URL — one URL for a whole catalogue.
func TestSceneURLAnchorsTheListingPage(t *testing.T) {
	cases := []struct{ pageURL, base, id, want string }{
		{"https://spankingbailey.com/updates.php", "https://spankingbailey.com", "1234", "https://spankingbailey.com/updates.php#1234"},
		{"", "https://spankingbailey.com", "1234", "https://spankingbailey.com#1234"},
		{"https://spankingbailey.com/updates.php", "https://spankingbailey.com", "", "https://spankingbailey.com/updates.php"},
	}
	for _, c := range cases {
		if got := sceneURL(c.pageURL, c.base, c.id); got != c.want {
			t.Errorf("sceneURL(%q, %q, %q) = %q, want %q", c.pageURL, c.base, c.id, got, c.want)
		}
	}
}

func TestBuildSceneUsesTheListingPage(t *testing.T) {
	s := New(SiteConfig{SiteID: "spankingbailey", Domain: "spankingbailey.com", StudioName: "Spanking Bailey", Type: TypeBailey})
	scene := s.buildScene(listingItem{id: "42", title: "A Scene"}, "https://spankingbailey.com/updates.php")
	if scene.URL != "https://spankingbailey.com/updates.php#42" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.ID != "42" || scene.Title != "A Scene" {
		t.Errorf("ID/Title = %q/%q", scene.ID, scene.Title)
	}
}

// A CMS that clamps an out-of-range page back to a real one serves a full page
// forever, so the walk must end on a page that repeats ids it already emitted.
func TestListScenesStopsWhenThePagerClamps(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/updates.php" {
			_, _ = fmt.Fprint(w, fixtureRSI)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	s := &Scraper{
		client: ts.Client(),
		base:   ts.URL,
		cfg:    SiteConfig{SiteID: "test-rsi", Domain: "test.com", StudioName: "Test RSI", Type: TypeRSI},
	}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan int)
	go func() {
		n := 0
		for r := range ch {
			if r.Kind == scraper.KindScene {
				n++
			}
		}
		done <- n
	}()

	select {
	case n := <-done:
		if n != 2 {
			t.Errorf("got %d scenes, want 2 — every page after the first repeats", n)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the walk did not terminate against a pager that clamps")
	}
}
