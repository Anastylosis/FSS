package cherrypimpsutil

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
	ID:       "testsite",
	SiteBase: "https://testsite.com",
	Studio:   "Test Studio",
	Patterns: []string{"testsite.com"},
	MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?testsite\.com`),
}

const testCardHTML = `<div class="item-update no-overlay item-video col-xxl-4 col-xl-4 col-lg-6 col-md-6 col-sm-6 col-xs-12 ">
	<div class="item-thumb">
<div class="item-video-thumb">
	<a href="https://cherrypimps.com/trailers/18574-averyblack.html"></a>
	<img src="https://cdn.cherrypimps.com/content/contentthumbs/24/80/142480-1x.jpg" alt="" class="video_placeholder" />
</div>
	</div>
	<div class="item-footer">
		<div class="item-row">
			<div class="item-title">
				<a href="https://cherrypimps.com/trailers/18574-averyblack.html" title="Following Command">
					Following Command				</a>
			</div>
		</div>
		<div class="item-row d-flex">
			<div class="item-date">
				24:30 | 96&nbsp;Photos | <i class="fa fa-calendar"></i> April 20, 2026			</div>
			<div class="item-sitename">
				<a href="https://cherrypimps.com/series/cucked.html" title="[Series] Cucked">Cucked</a>
			</div>
		</div>
		<div class="item-row">
			<div class="item-models">
				<a href="https://cherrypimps.com/models/AveryBlack.html">Avery Black</a>
				, <a href="https://cherrypimps.com/models/JackHunter.html">Jack Hunter</a>
				, <a href="https://cherrypimps.com/models/WillPounder.html">Will Pounder</a>
			</div>
		</div>
	</div>
</div><!--//item-update-->`

const testCardSinglePerf = `<div class="item-update no-overlay item-video col-xxl-4">
	<div class="item-thumb">
<div class="item-video-thumb">
	<a href="https://site.com/trailers/500-janesmith.html"></a>
	<img src="https://cdn.site.com/thumb-500.jpg" alt="" class="video_placeholder" />
</div>
	</div>
	<div class="item-footer">
		<div class="item-row">
			<div class="item-title">
				<a href="https://site.com/trailers/500-janesmith.html" title="Solo Scene">Solo Scene</a>
			</div>
		</div>
		<div class="item-row d-flex">
			<div class="item-date">
				12:45 | <i class="fa fa-calendar"></i> March 15, 2025			</div>
		</div>
		<div class="item-row">
			<div class="item-models">
				<a href="https://site.com/models/JaneSmith.html">Jane Smith</a>
			</div>
		</div>
	</div>
</div><!--//item-update-->`

func TestParseListingPage(t *testing.T) {
	body := []byte(testCardHTML + testCardSinglePerf)
	scenes := parseListingPage(body)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s := scenes[0]
	if s.id != "18574" {
		t.Errorf("id = %q, want 18574", s.id)
	}
	if s.title != "Following Command" {
		t.Errorf("title = %q, want %q", s.title, "Following Command")
	}
	if s.url != "https://cherrypimps.com/trailers/18574-averyblack.html" {
		t.Errorf("url = %q", s.url)
	}
	if s.thumb != "https://cdn.cherrypimps.com/content/contentthumbs/24/80/142480-1x.jpg" {
		t.Errorf("thumb = %q", s.thumb)
	}
	if s.date.Format("2006-01-02") != "2026-04-20" {
		t.Errorf("date = %v, want 2026-04-20", s.date)
	}
	if s.duration != 1470 {
		t.Errorf("duration = %d, want 1470", s.duration)
	}
	if len(s.performers) != 3 || s.performers[0] != "Avery Black" || s.performers[2] != "Will Pounder" {
		t.Errorf("performers = %v, want [Avery Black Jack Hunter Will Pounder]", s.performers)
	}

	s2 := scenes[1]
	if s2.id != "500" {
		t.Errorf("id = %q, want 500", s2.id)
	}
	if s2.duration != 765 {
		t.Errorf("duration = %d, want 765", s2.duration)
	}
	if len(s2.performers) != 1 || s2.performers[0] != "Jane Smith" {
		t.Errorf("performers = %v, want [Jane Smith]", s2.performers)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"24:30", 1470},
		{"5:00", 300},
		{"0:30", 30},
	}
	for _, tt := range tests {
		if got := parseutil.ParseDurationColon(tt.in); got != tt.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestEstimateTotal(t *testing.T) {
	body := []byte(`<a href="movies_5_d.html">5</a><a href="movies_303_d.html">303</a>`)
	if got := estimateTotal(body, 25); got != 7575 {
		t.Errorf("estimateTotal = %d, want 7575", got)
	}
}

func TestEstimateTotalSeries(t *testing.T) {
	body := []byte(`<a href="cucked_3_d.html">3</a>`)
	if got := estimateTotal(body, 24); got != 72 {
		t.Errorf("estimateTotal = %d, want 72", got)
	}
}

func TestParseStudioURL(t *testing.T) {
	tests := []struct {
		url      string
		wantMode listingMode
		wantSlug string
	}{
		{"https://cherrypimps.com/", modeFullCatalog, ""},
		{"https://cherrypimps.com", modeFullCatalog, ""},
		{"https://cherrypimps.com/categories/movies_1_d.html", modeFullCatalog, ""},
		{"https://cherrypimps.com/series/cucked.html", modeSeries, "cucked"},
		{"https://cherrypimps.com/series/wild-on-cam.html", modeSeries, "wild-on-cam"},
		{"https://cherrypimps.com/series/cucked_2_d.html", modeSeries, "cucked"},
		{"https://cherrypimps.com/categories/blowjob_1_d.html", modeCategory, "blowjob"},
		{"https://cherrypimps.com/models/AveryBlack.html", modeModel, "AveryBlack"},
		{"https://wildoncam.com/models/JaneDoe.html", modeModel, "JaneDoe"},
		{"https://cherrypimps.com/dvds/dvds.html", modeDVDListing, ""},
		{"https://cherrypimps.com/dvds/daddy-complex.html", modeDVD, "daddy-complex"},
		{"https://wildoncam.com/dvds/dvds.html", modeDVDListing, ""},
		{"https://wildoncam.com/dvds/some-title.html", modeDVD, "some-title"},
	}
	for _, tt := range tests {
		lc := parseStudioURL(tt.url)
		if lc.mode != tt.wantMode {
			t.Errorf("parseStudioURL(%q) mode = %d, want %d", tt.url, lc.mode, tt.wantMode)
		}
		if lc.slug != tt.wantSlug {
			t.Errorf("parseStudioURL(%q) slug = %q, want %q", tt.url, lc.slug, tt.wantSlug)
		}
	}
}

func TestListingConfigPageURL(t *testing.T) {
	tests := []struct {
		lc   listingConfig
		page int
		want string
	}{
		{listingConfig{modeFullCatalog, ""}, 1, "https://test.com/categories/movies_1_d.html"},
		{listingConfig{modeSeries, "cucked"}, 2, "https://test.com/series/cucked_2_d.html"},
		{listingConfig{modeCategory, "blowjob"}, 3, "https://test.com/categories/blowjob_3_d.html"},
	}
	for _, tt := range tests {
		got := tt.lc.pageURL("https://test.com", tt.page)
		if got != tt.want {
			t.Errorf("pageURL(%v, %d) = %q, want %q", tt.lc, tt.page, got, tt.want)
		}
	}
}

const cardTpl = `<div class="item-update no-overlay item-video col-xxl-4">
	<div class="item-thumb">
<div class="item-video-thumb">
	<a href="/trailers/%d-test.html"></a>
	<img src="/thumbs/%d.jpg" alt="" class="video_placeholder" />
</div>
	</div>
	<div class="item-footer">
		<div class="item-row">
			<div class="item-title">
				<a href="/trailers/%d-test.html" title="Scene %d">Scene %d</a>
			</div>
		</div>
		<div class="item-row d-flex">
			<div class="item-date">10:00 | January 1, 2025</div>
		</div>
		<div class="item-row">
			<div class="item-models">
				<a href="/models/Test.html">Test</a>
			</div>
		</div>
	</div>
</div><!--//item-update-->`

func buildTestPage(ids []int, maxPage int) []byte {
	var sb string
	for _, id := range ids {
		sb += fmt.Sprintf(cardTpl, id, id, id, id, id)
	}
	pager := ""
	for p := 2; p <= maxPage; p++ {
		pager += fmt.Sprintf(`<a href="movies_%d_d.html">%d</a>`, p, p)
	}
	return []byte(pager + sb)
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

		case strings.Contains(r.URL.Path, "/series/"):
			pageNum := extractPageNum(r.URL.Path)
			idx := pageNum - 1
			if idx >= 0 && idx < len(pages) {
				_, _ = w.Write(buildTestPage(pages[idx], len(pages)))
			} else {
				_, _ = fmt.Fprint(w, `<div>empty</div>`)
			}

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

func TestRun(t *testing.T) {
	ts := newTestServer([][]int{{100, 200}})
	defer ts.Close()

	cfg := testCfg
	cfg.SiteBase = ts.URL

	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
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

	cfg := testCfg
	cfg.SiteBase = ts.URL

	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
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

	cfg := testCfg
	cfg.SiteBase = ts.URL

	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
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

	cfg := testCfg
	cfg.SiteBase = ts.URL

	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/models/TestModel.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 3 {
		t.Fatalf("got %d scenes, want 3", len(got))
	}
}

const testDVDCardHTML = `<div class="item-update no-overlay item-model col">
	<div class="item-thumb">
		<img src="https://www.pulsedistribution.com/uploads/title/44765_med_f.jpg" alt="Daddy Complex" class="dvd_cover_placeholder" />
		<div class="flip-card"><div class="flip-card-inner">
			<div class="flip-card-front"><a href="https://cherrypimps.com/dvds/daddy-complex.html" title="Daddy Complex"><img src="img.jpg" alt="" /></a></div>
			<div class="flip-card-back"><a href="https://cherrypimps.com/dvds/daddy-complex.html" title="Daddy Complex"><img src="img_b.jpg" alt="" /></a></div>
		</div></div>
	</div>
	<div class="item-footer">
		<div class="item-row"><div class="item-title"><a href="https://cherrypimps.com/dvds/daddy-complex.html" title="Daddy Complex">Daddy Complex</a></div></div>
		<div class="item-row"><div class="item-date"><i class="fa fa-calendar"></i> <strong>Released:</strong> December 29, 2020</div></div>
	</div>
</div><!--//item-update-->
<div class="item-update no-overlay item-model col">
	<div class="item-thumb">
		<img src="img2.jpg" alt="Bush Friends Forever" class="dvd_cover_placeholder" />
		<div class="flip-card"><div class="flip-card-inner">
			<div class="flip-card-front"><a href="https://cherrypimps.com/dvds/bush-friends-forever.html" title="Bush Friends Forever"><img src="img2.jpg" alt="" /></a></div>
		</div></div>
	</div>
	<div class="item-footer">
		<div class="item-row"><div class="item-title"><a href="https://cherrypimps.com/dvds/bush-friends-forever.html" title="Bush Friends Forever">Bush Friends Forever</a></div></div>
		<div class="item-row"><div class="item-date"><i class="fa fa-calendar"></i> <strong>Released:</strong> May 1, 2020</div></div>
	</div>
</div><!--//item-update-->`

func TestParseDVDCards(t *testing.T) {
	urls := parseDVDCards([]byte(testDVDCardHTML))
	if len(urls) != 2 {
		t.Fatalf("got %d DVDs, want 2", len(urls))
	}
	if urls[0] != "https://cherrypimps.com/dvds/daddy-complex.html" {
		t.Errorf("dvd[0] = %q", urls[0])
	}
	if urls[1] != "https://cherrypimps.com/dvds/bush-friends-forever.html" {
		t.Errorf("dvd[1] = %q", urls[1])
	}
}

func TestParseDVDCardsDedups(t *testing.T) {
	doubled := testDVDCardHTML + testDVDCardHTML
	urls := parseDVDCards([]byte(doubled))
	if len(urls) != 2 {
		t.Fatalf("got %d DVDs, want 2 (deduped)", len(urls))
	}
}

func TestEstimateDVDTotal(t *testing.T) {
	body := []byte(`<a href="dvds_page_2.html">2</a><a href="dvds_page_3.html">3</a>`)
	if got := estimateDVDTotal(body); got != 3 {
		t.Errorf("estimateDVDTotal = %d, want 3", got)
	}
}

func TestEstimateDVDTotalNoPagination(t *testing.T) {
	body := []byte(`<div>no pagination links</div>`)
	if got := estimateDVDTotal(body); got != 1 {
		t.Errorf("estimateDVDTotal = %d, want 1", got)
	}
}

func TestDVDSinglePage(t *testing.T) {
	ts := newTestServer([][]int{{10, 20, 30}})
	defer ts.Close()

	cfg := testCfg
	cfg.SiteBase = ts.URL

	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/dvds/some-title.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 3 {
		t.Fatalf("got %d scenes, want 3", len(got))
	}
}

func TestDVDListing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/dvds/dvds.html":
			_, _ = fmt.Fprintf(w, `
				<div class="item-update no-overlay item-model col">
					<div class="item-thumb"><a href="%s/dvds/dvd-a.html" title="DVD A"><img class="dvd_cover_placeholder" /></a></div>
					<div class="item-footer"><div class="item-row"><div class="item-title"><a href="%s/dvds/dvd-a.html" title="DVD A">DVD A</a></div></div></div>
				</div><!--//item-update-->
				<div class="item-update no-overlay item-model col">
					<div class="item-thumb"><a href="%s/dvds/dvd-b.html" title="DVD B"><img class="dvd_cover_placeholder" /></a></div>
					<div class="item-footer"><div class="item-row"><div class="item-title"><a href="%s/dvds/dvd-b.html" title="DVD B">DVD B</a></div></div></div>
				</div><!--//item-update-->`, base, base, base, base)
		case "/dvds/dvd-a.html":
			_, _ = w.Write(buildTestPage([]int{100, 200}, 1))
		case "/dvds/dvd-b.html":
			_, _ = w.Write(buildTestPage([]int{300}, 1))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cfg := testCfg
	cfg.SiteBase = ts.URL

	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/dvds/dvds.html", scraper.ListOpts{
		Delay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 3 {
		t.Fatalf("got %d scenes, want 3", len(got))
	}
	if got[0].ID != "100" || got[1].ID != "200" || got[2].ID != "300" {
		t.Errorf("scene IDs = [%s, %s, %s], want [100, 200, 300]", got[0].ID, got[1].ID, got[2].ID)
	}
}

// A DVD is a compilation, so a stored scene sits in the middle of a disc that
// also carries new ones. The walk must skip it rather than stop.
func TestDVDListingKnownIDsSkipsWithoutTruncating(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/dvds/dvds.html":
			_, _ = fmt.Fprintf(w, `
				<div class="item-update no-overlay item-model col">
					<div class="item-thumb"><a href="%s/dvds/dvd-a.html" title="A"><img class="dvd_cover_placeholder" /></a></div>
					<div class="item-footer"><div class="item-row"><div class="item-title"><a href="%s/dvds/dvd-a.html" title="A">A</a></div></div></div>
				</div><!--//item-update-->`, base, base)
		case "/dvds/dvd-a.html":
			_, _ = w.Write(buildTestPage([]int{1, 2, 3}, 1))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cfg := testCfg
	cfg.SiteBase = ts.URL

	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/dvds/dvds.html", scraper.ListOpts{
		KnownIDs: map[string]bool{"2": true},
		Delay:    time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, stopped := testutil.CollectScenesWithStop(t, ch)
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2 — the known scene is skipped, not a stop", len(got))
	}
	if got[0].ID != "1" || got[1].ID != "3" {
		t.Errorf("scene IDs = [%s, %s], want [1, 3]", got[0].ID, got[1].ID)
	}
	if !stopped {
		t.Error("expected StoppedEarly to report the skipped scene")
	}
}

func TestSeriesPage(t *testing.T) {
	page1 := []int{10, 20}
	page2 := []int{30}

	ts := newTestServer([][]int{page1, page2})
	defer ts.Close()

	cfg := testCfg
	cfg.SiteBase = ts.URL

	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/series/cucked.html", scraper.ListOpts{
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

// A wholly-known DVD sits in front of the discs that carry new scenes, so it
// must not end the walk — only exhausting the DVD list does.
func TestDVDListingKnownDVDDoesNotAbortTheWalk(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/dvds/dvds.html":
			for _, name := range []string{"old", "new"} {
				_, _ = fmt.Fprintf(w, `
					<div class="item-update no-overlay item-model col">
						<div class="item-thumb"><a href="%s/dvds/%s.html" title="%s"><img class="dvd_cover_placeholder" /></a></div>
						<div class="item-footer"><div class="item-row"><div class="item-title"><a href="%s/dvds/%s.html" title="%s">%s</a></div></div></div>
					</div><!--//item-update-->`, base, name, name, base, name, name, name)
			}
		case "/dvds/old.html":
			_, _ = w.Write(buildTestPage([]int{1, 2}, 1))
		case "/dvds/new.html":
			_, _ = w.Write(buildTestPage([]int{3}, 1))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cfg := testCfg
	cfg.SiteBase = ts.URL

	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/dvds/dvds.html", scraper.ListOpts{
		KnownIDs: map[string]bool{"1": true, "2": true},
		Delay:    time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, stopped := testutil.CollectScenesWithStop(t, ch)
	if len(got) != 1 || got[0].ID != "3" {
		t.Fatalf("scenes = %+v, want just the new DVD's scene 3", got)
	}
	if !stopped {
		t.Error("expected StoppedEarly to report the skipped scenes")
	}
}
