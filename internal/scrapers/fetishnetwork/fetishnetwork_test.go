package fetishnetwork

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

var _ scraper.StudioScraper = (*Scraper)(nil)

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.fetishnetwork.com/", true},
		{"https://fetishnetwork.com/t2/show.php?a=1765_1", true},
		{"http://www.fetishnetwork.com/t2/show.php?a=2040_1", true},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

const testListingHTML = `
<!-- start_link -->
<div class="col-sm-6 latest-section-back">
	<div class="row border-aera">
		<div class="col-sm-12 content-latest-img">
			<a href="refstat.php?lid=38881&sid=1765"><img src="faceimages/display1471034069.jpg" alt="Scene One Title"></a>
		</div>
		<div class="col-sm-12 content-latest-text">
			<div class="sub-content-main">
				<p class="text-infos img-name"><a href="refstat.php?lid=38881&sid=1765">Marina Angel&#039;s Lesbian DP</a></p>
				<div class="col-sm-6 sub-content-left">
					<p class="text-infos type-of-sex"><a href="show.php?a=1968_1">StrapOnSquad.com</a></p>
					<p class="text-infos view-rating"><span>Views: (21933)</span> <span>Rating: 4.00</span></p>
				</div>
				<div class="col-sm-6 text-right sub-content-right">
					<p class="text-infos type-of-sex"><a href="show.php?a=1971_1">Lesbian Domination</a></p>
					<p class="text-infos date-info">September 6, 2024</p>
				</div>
			</div>
		</div>
	</div>
</div>
<!-- end_link -->
<!-- start_link -->
<div class="col-sm-6 latest-section-back">
	<div class="row border-aera">
		<div class="col-sm-12 content-latest-img">
			<a href="refstat.php?lid=38756&sid=1765"><img src="faceimages/display1471418166.jpg" alt="Scene Two"></a>
		</div>
		<div class="col-sm-12 content-latest-text">
			<div class="sub-content-main">
				<p class="text-infos img-name"><a href="refstat.php?lid=38756&sid=1765">Charlie Stevens Endures Rough Sex &amp; Bondage</a></p>
				<div class="col-sm-6 sub-content-left">
					<p class="text-infos type-of-sex"><a href="show.php?a=1954_1">SexualDisgrace.com</a></p>
					<p class="text-infos view-rating"><span>Views: (17198)</span> <span>Rating: 3.72</span></p>
				</div>
				<div class="col-sm-6 text-right sub-content-right">
					<p class="text-infos type-of-sex"><a href="show.php?a=2037_1">Bondage / BDSM</a></p>
					<p class="text-infos date-info">August 29, 2024</p>
				</div>
			</div>
		</div>
	</div>
</div>
<!-- end_link -->
<a href="show.php?a=1765_2">2</a>
<a href="show.php?a=1765_44">44</a>
`

func TestParseListingPage(t *testing.T) {
	items := parseListingPage([]byte(testListingHTML))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	s := items[0]
	if s.id != "38881" {
		t.Errorf("id = %q, want 38881", s.id)
	}
	if s.title != "Marina Angel's Lesbian DP" {
		t.Errorf("title = %q, want %q", s.title, "Marina Angel's Lesbian DP")
	}
	if s.thumb != siteBase+"/t2/faceimages/display1471034069.jpg" {
		t.Errorf("thumb = %q", s.thumb)
	}
	if s.date.Format("2006-01-02") != "2024-09-06" {
		t.Errorf("date = %v, want 2024-09-06", s.date)
	}
	if s.subSite != "StrapOnSquad.com" {
		t.Errorf("subSite = %q, want StrapOnSquad.com", s.subSite)
	}
	if s.category != "Lesbian Domination" {
		t.Errorf("category = %q, want Lesbian Domination", s.category)
	}

	s2 := items[1]
	if s2.id != "38756" {
		t.Errorf("id = %q, want 38756", s2.id)
	}
	if s2.title != "Charlie Stevens Endures Rough Sex & Bondage" {
		t.Errorf("title = %q", s2.title)
	}
}

func TestParseListingPageDedup(t *testing.T) {
	doubled := testListingHTML + testListingHTML
	items := parseListingPage([]byte(doubled))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (deduped)", len(items))
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"September 6, 2024", "2024-09-06"},
		{"August 29, 2024", "2024-08-29"},
		{"January 15, 2020", "2020-01-15"},
	}
	for _, tt := range tests {
		got := parseDate(tt.in)
		if got.Format("2006-01-02") != tt.want {
			t.Errorf("parseDate(%q) = %v, want %s", tt.in, got, tt.want)
		}
	}
}

func TestEstimateTotal(t *testing.T) {
	body := []byte(`<a href="show.php?a=1765_2">2</a><a href="show.php?a=1765_44">44</a>`)
	if got := estimateTotal(body, 10); got != 440 {
		t.Errorf("estimateTotal = %d, want 440", got)
	}
}

func TestToScene(t *testing.T) {
	item := sceneItem{
		id:       "12345",
		title:    "Test Scene",
		thumb:    siteBase + "/t2/faceimages/test.jpg",
		date:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		subSite:  "BrutalPOV.com",
		category: "Bondage / BDSM",
	}
	sc := item.toScene("https://www.fetishnetwork.com", time.Now().UTC())
	if sc.ID != "12345" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != "fetishnetwork" {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.Series != "BrutalPOV" {
		t.Errorf("Series = %q, want BrutalPOV", sc.Series)
	}
	if len(sc.Tags) != 1 || sc.Tags[0] != "Bondage / BDSM" {
		t.Errorf("Tags = %v", sc.Tags)
	}
}

func TestCatIDFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.fetishnetwork.com/t2/show.php?a=2040_1", "2040"},
		{"https://www.fetishnetwork.com/t2/show.php?a=1968_1", "1968"},
		{"https://www.fetishnetwork.com/", ""},
	}
	for _, tt := range tests {
		m := catIDRe.FindStringSubmatch(tt.url)
		got := ""
		if m != nil {
			got = m[1]
		}
		if got != tt.want {
			t.Errorf("catID(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

const cardTpl = `<!-- start_link -->
<div class="col-sm-6 latest-section-back">
	<div class="row border-aera">
		<div class="col-sm-12 content-latest-img">
			<a href="refstat.php?lid=%d&sid=1765"><img src="faceimages/face%d.jpg" alt=""></a>
		</div>
		<div class="col-sm-12 content-latest-text">
			<div class="sub-content-main">
				<p class="text-infos img-name"><a href="refstat.php?lid=%d&sid=1765">Scene %d</a></p>
				<div class="col-sm-6 sub-content-left">
					<p class="text-infos type-of-sex"><a href="show.php?a=1968_1">TestSite.com</a></p>
					<p class="text-infos view-rating"><span>Views: (100)</span> <span>Rating: 3.50</span></p>
				</div>
				<div class="col-sm-6 text-right sub-content-right">
					<p class="text-infos type-of-sex"><a href="show.php?a=2037_1">BDSM</a></p>
					<p class="text-infos date-info">January 1, 2025</p>
				</div>
			</div>
		</div>
	</div>
</div>
<!-- end_link -->`

func buildPage(ids []int, maxPage int) string {
	var sb string
	for _, id := range ids {
		sb += fmt.Sprintf(cardTpl, id, id, id, id)
	}
	for p := 2; p <= maxPage; p++ {
		sb += fmt.Sprintf(`<a href="show.php?a=1765_%d">%d</a>`, p, p)
	}
	return sb
}

func TestRun(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		a := r.URL.Query().Get("a")
		switch a {
		case "1765_1":
			_, _ = fmt.Fprint(w, buildPage([]int{100, 200}, 1))
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	s := New()
	ch, err := s.ListScenes(context.Background(), ts.URL+"/t2/show.php?a=1765_1", scraper.ListOpts{
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
}

func TestPagination(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		a := r.URL.Query().Get("a")
		switch a {
		case "1765_1":
			_, _ = fmt.Fprint(w, buildPage([]int{10, 20}, 2))
		case "1765_2":
			_, _ = fmt.Fprint(w, buildPage([]int{30}, 2))
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	s := New()
	ch, err := s.ListScenes(context.Background(), ts.URL+"/t2/show.php?a=1765_1", scraper.ListOpts{
		Delay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 3 {
		t.Fatalf("got %d scenes, want 3", len(got))
	}
}

func TestKnownIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, buildPage([]int{1, 2, 3, 4}, 1))
	}))
	defer ts.Close()

	s := New()
	ch, err := s.ListScenes(context.Background(), ts.URL+"/t2/show.php?a=1765_1", scraper.ListOpts{
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
