package onepassforallsites

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://1passforallsites.com/", true},
		{"https://1passforallsites.com/scenes", true},
		{"https://1passforallsites.com/episode/5036/sex-at-a-private-lesson", true},
		{"https://oldgoesyoung.com/", true},
		{"https://www.oldgoesyoung.com/", true},
		{"https://trickyoldteacher.com/", true},
		{"https://18virginsex.com/", true},
		{"https://spoiledvirgins.com/", true},
		{"https://younganaltryouts.com/", true},
		{"https://other.com/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestParseListingCardsHub(t *testing.T) {
	body := []byte(`<ul class="thumbs tn-scenes">
<li>
<a class="tn" href="https://1passforallsites.com/episode/5130/gentle-massage" title="Brunette gives massage"><p class="tn-views"><span>1,234</span> views</p><img src='https://img10.thepluginz.com/paysite/29/thumb.jpg' alt="alt text" border=0 /></a>
<p class="tn-title"><a href="/model?id=1545">Alita Angel <span>19y.o.</span></a></p>
<p class="tn-rank">8.05</p>
<p class="tn-desc">Alita loves taking care of her boyfriend.</p>
<p class="tn-added"><span>added </span>27 Mar 2026</p>
<p class="tn-source"><span>from: </span><a href="/join">Young Anal Tryouts</a></p>
</li>
<li>
<a class="tn" href="https://1passforallsites.com/episode/5091/sex-and-computers" title="Hottie shows her round ass"><p class="tn-views"><span>2,729</span> views</p><img src='https://img10.thepluginz.com/paysite/23/thumb2.jpg' alt="alt2" border=0 /></a>
<p class="tn-title"><a href="/model?id=1503">Vilora Efi <span>18y.o.</span></a></p>
<p class="tn-rank">7.23</p>
<p class="tn-desc">An amazing teacher lesson.</p>
<p class="tn-added"><span>added </span>17 May 2024</p>
<p class="tn-source"><span>from: </span><a href="/join">Tricky Old Teacher</a></p>
</li>
</ul>`)

	items := parseListingCards(body, hubBase)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	i := items[0]
	if i.id != "5130" {
		t.Errorf("id = %q", i.id)
	}
	if i.slug != "gentle-massage" {
		t.Errorf("slug = %q", i.slug)
	}
	if i.title != "Brunette gives massage" {
		t.Errorf("title = %q", i.title)
	}
	if i.thumb != "https://img10.thepluginz.com/paysite/29/thumb.jpg" {
		t.Errorf("thumb = %q", i.thumb)
	}
	if i.performer != "Alita Angel" {
		t.Errorf("performer = %q", i.performer)
	}
	if i.date != "27 Mar 2026" {
		t.Errorf("date = %q", i.date)
	}
	if i.series != "Young Anal Tryouts" {
		t.Errorf("series = %q", i.series)
	}
	if i.url != hubBase+"/episode/5130/gentle-massage" {
		t.Errorf("url = %q", i.url)
	}
}

func TestParseListingCardsChildSite(t *testing.T) {
	body := []byte(`<ul class="thumbs tn-updates">
<li>
<a class="tn" href="https://oldgoesyoung.com/episode/5142/sudden-meeting"><img src="http://img10.thepluginz.com/paysite/30/thumb.jpg" alt="alt" title="Reina Flore, 18 - Sudden meeting"></a>
<p class="tn-title"><a href="https://oldgoesyoung.com/episode/5142/sudden-meeting" title="Sudden meeting">Sudden meeting…</a></p>
<p class="tn-models"><a href="https://oldgoesyoung.com/episode/5142/sudden-meeting" title="Sudden meeting">Reina Flore</a></p>
<p class="tn-added">Added: <span>15 May 2026</span></p>
<p class="tn-rank">Rating: <span>7.43</span></p>
</li>
</ul>`)

	items := parseListingCards(body, "https://oldgoesyoung.com")
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].id != "5142" {
		t.Errorf("id = %q", items[0].id)
	}
	if items[0].performer != "Reina Flore" {
		t.Errorf("performer = %q", items[0].performer)
	}
	if items[0].date != "15 May 2026" {
		t.Errorf("date = %q", items[0].date)
	}
	if items[0].url != "https://oldgoesyoung.com/episode/5142/sudden-meeting" {
		t.Errorf("url = %q", items[0].url)
	}
}

func TestParseListingCardsDedupe(t *testing.T) {
	body := []byte(`
<a class="tn" href="/episode/100/scene-a" title="Scene A"><img src='https://img10.thepluginz.com/t.jpg'></a></li>
<a class="tn" href="/episode/100/scene-a" title="Scene A dup"><img src='https://img10.thepluginz.com/t2.jpg'></a></li>`)

	items := parseListingCards(body, hubBase)
	if len(items) != 1 {
		t.Errorf("got %d items, want 1 (dedup)", len(items))
	}
}

func TestParseDetailPage(t *testing.T) {
	body := []byte(`<html>
<p class="path"><a href="/">Home</a> <a href="/scenes">Tricky Old Teacher</a>   Sex at a private lesson</p>
<div class="sp-info">
<p class="sp-info-name"><a href="/model?id=1503">Vilora Efi <span>19 y.o.</span></a></p>
</div>
<h3>Description</h3>
<p>Vilora Efi reads the test in the textbook, trying to understand one of the new topics.</p>
<h3>Niches</h3>
<p class="niches-list"><a href="/scenes/niches?niche=8">Old and Young</a>, <a href="/scenes/niches?niche=1">Hardcore</a>, <a href="/scenes/niches?niche=15">Teen</a></p>
</html>`)

	d := parseDetailPage(body)
	if d.description != "Vilora Efi reads the test in the textbook, trying to understand one of the new topics." {
		t.Errorf("description = %q", d.description)
	}
	if len(d.tags) != 3 || d.tags[0] != "Old and Young" || d.tags[1] != "Hardcore" || d.tags[2] != "Teen" {
		t.Errorf("tags = %v", d.tags)
	}
	if d.performer != "Vilora Efi" {
		t.Errorf("performer = %q", d.performer)
	}
	if d.series != "Tricky Old Teacher" {
		t.Errorf("series = %q", d.series)
	}
}

func TestParseDetailPageEmpty(t *testing.T) {
	d := parseDetailPage([]byte(`<html><body>no detail here</body></html>`))
	if d.description != "" || len(d.tags) != 0 || d.performer != "" {
		t.Errorf("expected empty detail, got %+v", d)
	}
}

func TestParseTotalPages(t *testing.T) {
	body := []byte(`<a href="?page=2&site=0">2</a><a href="?page=8&site=0">8</a>`)
	if got := parseTotalPages(body); got != 8 {
		t.Errorf("parseTotalPages = %d, want 8", got)
	}
}

func TestResolveBase(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want string
	}{
		{"https://1passforallsites.com/", hubBase},
		{"https://1passforallsites.com/scenes", hubBase},
		{"https://oldgoesyoung.com/", "https://oldgoesyoung.com"},
		{"https://www.trickyoldteacher.com/", "https://trickyoldteacher.com"},
		{"https://other.com/", hubBase},
	}
	for _, c := range cases {
		if got := s.resolveBase(c.url); got != c.want {
			t.Errorf("resolveBase(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

const listingTpl = `<a class="tn" href="%s/episode/%d/scene-%d" title="Scene %d"><img src='https://img10.thepluginz.com/thumb-%d.jpg'></a>
<p class="tn-title"><a href="/model?id=%d">Performer %d </a></p>
<p class="tn-desc">Description %d.</p>
<p class="tn-added"><span>added </span>15 Jan 2026</p>
<p class="tn-source"><span>from: </span><a href="/join">Test Studio</a></p>
</li>`

const detailTpl = `<html>
<p class="path"><a href="/">Home</a> <a href="/scenes">Test Studio</a>   Scene %d</p>
<div class="sp-info">
<p class="sp-info-name"><a href="/model?id=%d">Performer %d <span>20 y.o.</span></a></p>
</div>
<h3>Description</h3>
<p>Full description for scene %d.</p>
<h3>Niches</h3>
<p class="niches-list"><a href="#">Tag1</a>, <a href="#">Tag2</a></p>
</html>`

func buildListingPage(base string, ids []int) string {
	var sb strings.Builder
	for _, id := range ids {
		_, _ = fmt.Fprintf(&sb, listingTpl, base, id, id, id, id, id, id, id)
	}
	return sb.String()
}

func newTestServer(ids []int) *httptest.Server {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch {
		case r.URL.Path == "/scenes" || r.URL.Path == "/":
			_, _ = fmt.Fprint(w, buildListingPage(ts.URL, ids))
		case strings.HasPrefix(r.URL.Path, "/episode/"):
			var id int
			_, _ = fmt.Sscanf(r.URL.Path, "/episode/%d/", &id)
			if id > 0 {
				_, _ = fmt.Fprintf(w, detailTpl, id, id, id, id)
			} else {
				w.WriteHeader(404)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	return ts
}

func TestListScenes(t *testing.T) {
	ts := newTestServer([]int{3, 2, 1})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 3 {
		t.Fatalf("got %d scenes, want 3", len(results))
	}
	for _, sc := range results {
		if sc.SiteID != siteID {
			t.Errorf("siteID = %q", sc.SiteID)
		}
		if sc.Studio != studioName {
			t.Errorf("studio = %q", sc.Studio)
		}
		if len(sc.Performers) != 1 {
			t.Errorf("performers = %v", sc.Performers)
		}
		if len(sc.Tags) != 2 {
			t.Errorf("tags = %v", sc.Tags)
		}
		if sc.Series != "Test Studio" {
			t.Errorf("series = %q", sc.Series)
		}
		if sc.Description == "" {
			t.Error("expected non-empty description")
		}
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	ts := newTestServer([]int{3, 2, 1})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"2": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(results) != 1 {
		t.Fatalf("got %d scenes, want 1", len(results))
	}
}

func TestScraperInterface(t *testing.T) {
	var _ scraper.StudioScraper = New()
}

// The hub serves 38 cards a page and the child domains 18. A hardcoded page
// size read every child's first page as its last, truncating 30 catalogues to
// their first 18 scenes — which --full's authoritative Save then treats as the
// whole studio.
func TestChildDomainPageSizeIsNotHardcoded(t *testing.T) {
	page1 := []int{1, 2, 3, 4, 5}
	page2 := []int{6, 7}

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch {
		case strings.HasPrefix(r.URL.Path, "/episode/"):
			var id int
			_, _ = fmt.Sscanf(r.URL.Path, "/episode/%d/", &id)
			_, _ = fmt.Fprintf(w, detailTpl, id, id, id, id)
		case r.URL.Query().Get("page") == "2":
			_, _ = fmt.Fprint(w, buildListingPage(ts.URL, page2))
		default:
			_, _ = fmt.Fprint(w, buildListingPage(ts.URL, page1))
		}
	}))
	defer ts.Close()

	s := New()
	s.base = ts.URL
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	ids := map[string]bool{}
	for res := range ch {
		if res.Kind == scraper.KindScene {
			ids[res.Scene.ID] = true
		}
	}
	if len(ids) != len(page1)+len(page2) {
		t.Errorf("got %d scenes, want %d — page 2 must still be walked", len(ids), len(page1)+len(page2))
	}
}

// A listing that ignores ?page= must not loop forever.
func TestRepeatedPageStopsTheWalk(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if strings.HasPrefix(r.URL.Path, "/episode/") {
			var id int
			_, _ = fmt.Sscanf(r.URL.Path, "/episode/%d/", &id)
			_, _ = fmt.Fprintf(w, detailTpl, id, id, id, id)
			return
		}
		_, _ = fmt.Fprint(w, buildListingPage(ts.URL, []int{1, 2, 3}))
	}))
	defer ts.Close()

	s := New()
	s.base = ts.URL
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	scenes := 0
	for res := range ch {
		if res.Kind == scraper.KindScene {
			scenes++
		}
	}
	if scenes != 3 {
		t.Errorf("scenes = %d, want 3", scenes)
	}
}
