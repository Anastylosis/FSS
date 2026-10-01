package alisonangel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://alisonangel.com/", true},
		{"https://www.alisonangel.com/", true},
		{"https://alisonangel.com/episode/beautiful-wildflower-176.html", true},
		{"https://example.com/alisonangel", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestParseSlugID(t *testing.T) {
	cases := []struct {
		href string
		want string
	}{
		{"/episode/beautiful-wildflower-176.html", "176"},
		{"/episode/sensual-in-silver-175.html", "175"},
		{"/episode/intimate-closeups-174.html", "174"},
		{"/episode/hard-deep-glass-toysex-173.html", "173"},
		{"/episode/no-id.html", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := parseSlugID(c.href); got != c.want {
			t.Errorf("parseSlugID(%q) = %q, want %q", c.href, got, c.want)
		}
	}
}

func TestParseHomepage(t *testing.T) {
	body := []byte(`
<div class="epwrapper">
<div class="epbox">
<div class="episodepic2"><a href="/episode/beautiful-wildflower-176.html"><img src="/content/chapters/0111/v1/preview/episodemed.jpg" width="217" height="331" border="0" alt=""></a></div>
<div class="episodeinfo2 eptext">
<div class="eptitle"><a href="/episode/beautiful-wildflower-176.html">Beautiful Wildflower</a></div>
<img src="/images/ep_rating.png" width="100" height="19"><br>
<p><strong>Release Date:</strong> 2026-05-08<br>
<strong>Photo Count:</strong> 60<br>
<strong>Video Length:</strong> 6:39 mins</p>
<p>The open space and flowers.</p>
</div>
<div class="clear"></div>
</div>
<div class="epbox">
<div class="episodepic2"><a href="/episode/sensual-in-silver-175.html"><img src="/content/chapters/0110/v1/preview/episodemed.jpg" width="217" height="331" border="0" alt=""></a></div>
<div class="episodeinfo2 eptext">
<div class="eptitle"><a href="/episode/sensual-in-silver-175.html">Sensual In Silver</a></div>
<img src="/images/ep_rating.png"><br>
<p><strong>Release Date:</strong> 2026-04-24<br>
<strong>Photo Count:</strong> 45<br>
<strong>Video Length:</strong> 5:12 mins</p>
<p>Silver outfit photoshoot.</p>
</div>
<div class="clear"></div>
</div>
</div>
<div class="eplist">
<div class="eplistcell"><a href="/episode/intimate-closeups-174.html"><img src="/content/chapters/0109/v1/preview/episodesm.jpg" alt="" width="146" height="223" border="0"></a></div>
<div class="eplistcell"><a href="/episode/hard-deep-glass-toysex-173.html"><img src="/content/chapters/0108/v1/preview/episodesm.jpg" alt="" width="146" height="223" border="0"></a></div>
</div>`)

	featured, episodes := parseHomepage(body)

	if len(featured) != 2 {
		t.Fatalf("got %d featured, want 2", len(featured))
	}
	if len(episodes) != 4 {
		t.Fatalf("got %d episodes, want 4", len(episodes))
	}

	f := featured[0]
	if f.id != "176" {
		t.Errorf("id = %q, want 176", f.id)
	}
	if f.title != "Beautiful Wildflower" {
		t.Errorf("title = %q", f.title)
	}
	wantDate := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	if !f.date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", f.date, wantDate)
	}
	if f.dur != 399 {
		t.Errorf("dur = %d, want 399 (6*60+39)", f.dur)
	}
	if f.thumb != "/content/chapters/0111/v1/preview/episodemed.jpg" {
		t.Errorf("thumb = %q", f.thumb)
	}

	f2 := featured[1]
	if f2.id != "175" {
		t.Errorf("id = %q, want 175", f2.id)
	}
	if f2.title != "Sensual In Silver" {
		t.Errorf("title = %q", f2.title)
	}

	if episodes[0].id != "176" || episodes[0].path != "/episode/beautiful-wildflower-176.html" {
		t.Errorf("ep0 = %+v", episodes[0])
	}
	if episodes[2].id != "174" || episodes[2].path != "/episode/intimate-closeups-174.html" {
		t.Errorf("ep2 = %+v", episodes[2])
	}
	if episodes[3].id != "173" {
		t.Errorf("ep3 id = %q, want 173", episodes[3].id)
	}
}

func TestParseDetailPage(t *testing.T) {
	body := []byte(`
<div class="content">
<div class="episode">
<div class="episodepic"><a href="/join.php"><img src="/content/chapters/0111/v1/preview/episode.jpg" width="416" height="634" border="0" alt=""></a></div>
<div class="episodeinfo">
<div class="episodetitle"><a href="/join.php">Beautiful Wildflower</a></div>
<div class="ep_pics"><strong>60</strong> High Resolution Photos</div>
<div class="ep_vids"><strong>6:39 mins</strong> High Definition Video</div>
<div class="epdetails">The open space and all the wild flowers make me want to jump around.</div>
</div>
<div class="clear"></div>
<table><tr>
<td><a href="/join.php"><img src="/images/bt_getpass1.jpg"></a></td>
<td><a href="/episode/sensual-in-silver-175.html" onmouseout="MM_swapImgRestore()" onmouseover="MM_swapImage('Image56','','/images/bt_continue2.jpg',1)"><img src="/images/bt_continue1.jpg" name="Image56" width="356" height="49" border="0" alt=""></a></td>
</tr></table>
</div>
</div>`)

	d := parseDetailPage(body)
	if d.title != "Beautiful Wildflower" {
		t.Errorf("title = %q", d.title)
	}
	if d.dur != 399 {
		t.Errorf("dur = %d, want 399", d.dur)
	}
	if d.desc != "The open space and all the wild flowers make me want to jump around." {
		t.Errorf("desc = %q", d.desc)
	}
	if d.thumb != "/content/chapters/0111/v1/preview/episode.jpg" {
		t.Errorf("thumb = %q", d.thumb)
	}
	if d.nextPath != "/episode/sensual-in-silver-175.html" {
		t.Errorf("nextPath = %q", d.nextPath)
	}
}

func TestParseDetailPageNoNext(t *testing.T) {
	body := []byte(`
<div class="episodepic"><a href="/join.php"><img src="/content/chapters/0001/v1/preview/episode.jpg"></a></div>
<div class="episodetitle"><a href="/join.php">First Episode</a></div>
<div class="ep_vids"><strong>3:22 mins</strong> High Definition Video</div>
<div class="epdetails">The very first episode.</div>`)

	d := parseDetailPage(body)
	if d.title != "First Episode" {
		t.Errorf("title = %q", d.title)
	}
	if d.nextPath != "" {
		t.Errorf("nextPath = %q, want empty", d.nextPath)
	}
}

const detailTemplate = `<div class="episodepic"><a href="/join.php"><img src="/content/chapters/%04d/v1/preview/episode.jpg"></a></div>
<div class="episodetitle"><a href="/join.php">Episode %d</a></div>
<div class="ep_vids"><strong>5:00 mins</strong> High Definition Video</div>
<div class="epdetails">Description for episode %d.</div>
<a href="/episode/ep-%d.html" onmouseout="MM_swapImgRestore()"><img src="/images/bt_continue1.jpg"></a>`

func buildHomepage(ids []int) []byte {
	var sb string
	sb += `<div class="epwrapper">`
	for i, id := range ids {
		if i < 2 {
			sb += fmt.Sprintf(`
<div class="epbox">
<div class="episodepic2"><a href="/episode/ep-%d.html"><img src="/content/chapters/%04d/v1/preview/episodemed.jpg"></a></div>
<div class="episodeinfo2 eptext">
<div class="eptitle"><a href="/episode/ep-%d.html">Episode %d</a></div>
<p><strong>Release Date:</strong> 2026-01-15<br>
<strong>Photo Count:</strong> 30<br>
<strong>Video Length:</strong> 5:00 mins</p>
<p>Description %d.</p>
</div>
<div class="clear"></div>
</div>`, id, id, id, id, id)
		} else {
			sb += fmt.Sprintf(`
<div class="eplistcell"><a href="/episode/ep-%d.html"><img src="/content/chapters/%04d/v1/preview/episodesm.jpg"></a></div>`, id, id)
		}
	}
	sb += `</div>`
	return []byte(sb)
}

func newTestServer(ids []int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")

		switch r.URL.Path {
		case "/":
			_, _ = w.Write(buildHomepage(ids))
		default:
			var id int
			_, _ = fmt.Sscanf(r.URL.Path, "/episode/ep-%d.html", &id)
			if id < 1 {
				return
			}
			nextID := id - 1
			if nextID < 1 {
				_, _ = fmt.Fprintf(w, `<div class="episodepic"><a href="/join.php"><img src="/img/%d.jpg"></a></div>
<div class="episodetitle"><a href="/join.php">Episode %d</a></div>
<div class="ep_vids"><strong>5:00 mins</strong> High Definition Video</div>
<div class="epdetails">Desc %d.</div>`, id, id, id)
			} else {
				_, _ = fmt.Fprintf(w, detailTemplate, id, id, id, nextID)
			}
		}
	}))
}

func TestListScenes(t *testing.T) {
	ts := newTestServer([]int{5, 4, 3})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 5 {
		t.Fatalf("got %d scenes, want 5 (3 from homepage + 2 from chain)", len(results))
	}
	for _, sc := range results {
		if sc.SiteID != "alisonangel" {
			t.Errorf("siteID = %q", sc.SiteID)
		}
		if len(sc.Performers) != 1 || sc.Performers[0] != "Alison Angel" {
			t.Errorf("performers = %v", sc.Performers)
		}
		if !strings.HasPrefix(sc.Thumbnail, ts.URL+"/") {
			t.Errorf("thumbnail = %q, want absolute under %s", sc.Thumbnail, ts.URL)
		}
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	ts := newTestServer([]int{5, 4, 3})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"3": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2 (IDs 5,4 before known 3)", len(results))
	}
}

func TestListScenesEnrichment(t *testing.T) {
	ts := newTestServer([]int{5, 4})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	enriched := 0
	for _, sc := range results {
		if !sc.Date.IsZero() {
			enriched++
		}
	}
	if enriched != 2 {
		t.Errorf("got %d scenes with dates, want 2 (only featured eps have dates)", enriched)
	}
}

func TestScraperInterface(t *testing.T) {
	var _ scraper.StudioScraper = New()
}

func TestListScenesOffSiteRedirectIsReported(t *testing.T) {
	billing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body><form class="place_order"></form></body></html>`)
	}))
	defer billing.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, billing.URL+"/flexforms/x", http.StatusFound)
	}))
	defer site.Close()

	s := &Scraper{client: site.Client(), base: site.URL}
	ch, err := s.ListScenes(context.Background(), site.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var errs []error
	for r := range ch {
		if r.Kind == scraper.KindScene {
			t.Errorf("unexpected scene %+v", r.Scene)
		}
		if r.Err != nil {
			errs = append(errs, r.Err)
		}
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "redirected off-site") {
		t.Fatalf("errors = %v, want one off-site redirect error", errs)
	}
}
