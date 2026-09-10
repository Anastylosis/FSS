package xespl

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
		{"https://xes.pl/katalog_filmow,1.html", true},
		{"https://www.xes.pl/katalog_filmow,1.html", true},
		{"https://xes.pl/aktor,katarzyna-bella-donna,384,1.html", true},
		{"https://xes.pl/produkcja,xes-pl,1.html", true},
		{"https://xes.pl/filtr,4K,1.html", true},
		{"https://xes.pl/", true},
		{"https://example.com/xes", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	base := "https://xes.pl"
	cases := []struct {
		input string
		want  string
	}{
		{"https://xes.pl", base + "/katalog_filmow,1.html"},
		{"https://xes.pl/", base + "/katalog_filmow,1.html"},
		{"https://www.xes.pl", base + "/katalog_filmow,1.html"},
		{"https://xes.pl/katalog_filmow,1.html", "https://xes.pl/katalog_filmow,1.html"},
		{"https://xes.pl/aktor,slug,384,1.html", "https://xes.pl/aktor,slug,384,1.html"},
	}
	for _, c := range cases {
		if got := normalizeURL(base, c.input); got != c.want {
			t.Errorf("normalizeURL(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestBuildPageURL(t *testing.T) {
	cases := []struct {
		template string
		page     int
		want     string
	}{
		{"https://xes.pl/katalog_filmow,1.html", 5, "https://xes.pl/katalog_filmow,5.html"},
		{"https://xes.pl/aktor,slug,384,1.html", 3, "https://xes.pl/aktor,slug,384,3.html"},
		{"https://xes.pl/produkcja,xes-pl,1.html", 2, "https://xes.pl/produkcja,xes-pl,2.html"},
		{"https://xes.pl/filtr,4K,1.html", 10, "https://xes.pl/filtr,4K,10.html"},
	}
	for _, c := range cases {
		if got := buildPageURL(c.template, c.page); got != c.want {
			t.Errorf("buildPageURL(%q, %d) = %q, want %q", c.template, c.page, got, c.want)
		}
	}
}

func TestParseListingPage(t *testing.T) {
	body := []byte(`
<div class="flex-video-wrap">
<article class="videoCatalogCard">
<div>
<span class="video-4k pill">4K</span>
<div class="pictureWrap videoPreview">
<a href="epizod,7299,pelnia-kobiecosci.html" style="width:412px;max-width:100%;">
<picture>
<source srcset="/static/uploaded/video/7/72/7299/slider_org.webp" type="image/webp" />
<img src="/static/uploaded/video/7/72/7299/slider_org.jpg" alt="Pełnia kobiecości" width="412" height="190" />
</picture>
<span class="infoWrap">
<span class="pill">11 godzin temu</span>
<span class="pill">Price: <span>9 pts</span></span>
</span>
</a>
</div>
<div class="description">
<h2><a href="epizod,7299,pelnia-kobiecosci.html">Pełnia kobiecości</a></h2>
</div>
</div>
</div>
<article class="videoCatalogCard">
<div>
<div class="pictureWrap videoPreview">
<a href="epizod,7287,analne-zabawy-z-shanti.html" style="width:412px;max-width:100%;">
<img src="/static/uploaded/video/7/72/7287/slider_org.jpg" alt="Analne zabawy z Shanti" width="412" height="165">
<span class="infoWrap">
<span class="pill">1 dzień temu</span>
<span class="pill">Price: <span>15 pts</span></span>
</span>
</a>
</div>
<div class="description">
<h2><a href="epizod,7287,analne-zabawy-z-shanti.html">Analne zabawy z Shanti</a></h2>
</div>
</div>
</div>
</div>`)

	scenes := parseListingPage(body, "https://xes.pl")
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s := scenes[0]
	if s.id != "7299" {
		t.Errorf("id = %q, want 7299", s.id)
	}
	if s.url != "https://xes.pl/epizod,7299,pelnia-kobiecosci.html" {
		t.Errorf("url = %q", s.url)
	}
	if s.title != "Pełnia kobiecości" {
		t.Errorf("title = %q", s.title)
	}
	if s.thumb != "https://xes.pl/static/uploaded/video/7/72/7299/slider_org.jpg" {
		t.Errorf("thumb = %q", s.thumb)
	}

	s2 := scenes[1]
	if s2.id != "7287" {
		t.Errorf("id = %q, want 7287", s2.id)
	}
	if s2.title != "Analne zabawy z Shanti" {
		t.Errorf("title = %q", s2.title)
	}
}

func TestParseMaxPage(t *testing.T) {
	body := []byte(`<ul class="pagination">
<li class="active"><a href="katalog_filmow,1.html">1</a></li>
<li><a href="katalog_filmow,2.html">2</a></li>
<li class="disabled"><a>...</a></li>
<li><a href="katalog_filmow,175.html">175</a></li>
<li><a class="next" href="katalog_filmow,2.html">&raquo;</a></li>
</ul>`)
	if got := parseMaxPage(body); got != 175 {
		t.Errorf("parseMaxPage = %d, want 175", got)
	}
}

func TestParseMaxPageSingle(t *testing.T) {
	body := []byte(`<div>no pagination here</div>`)
	if got := parseMaxPage(body); got != 1 {
		t.Errorf("parseMaxPage = %d, want 1", got)
	}
}

func TestParseDetailPage(t *testing.T) {
	body := []byte(`
<h1 class="arrow">Pełnia kobiecości<span> / Epizod 477 Judyta</span></h1>
<article>
<p class="padding10">Judyta zaprasza swoich fanów do wspólnej zabawy.</p>
</article>
<div class="details">
<table>
<tr><td>Price:</td><td><span class="price">9 pts</span></td></tr>
<tr><td>Resolution:</td><td>3840x2160</td></tr>
<tr><td>Duration:</td><td>00:13:42</td></tr>
<tr><td>Add date:</td><td>2026-04-28</td></tr>
</table>
<div class="details_hidden">
<table>
<tr><td>Views:</td><td>5191</td></tr>
<tr><td>Producer:</td><td><a class="producerLink" href="produkcja,masturbowanie-pl,1.html">Masturbowanie</a></td></tr>
<tr><td>Categories:</td><td><ul><li><a href="filtr,4K.html">4K</a></li><li><a href="filtr,mamuski.html">MILF</a></li><li><a href="filtr,masturbacja.html">Masturbation</a></li></ul></td></tr>
<tr><td>Actors:</td><td><ul><li><a href="aktor,judyta,818.html">Judyta</a></li></ul></td></tr>
</table>
</div>
</div>`)

	d := parseDetailPage(body)

	if d.title != "Pełnia kobiecości" {
		t.Errorf("title = %q", d.title)
	}
	if d.description != "Judyta zaprasza swoich fanów do wspólnej zabawy." {
		t.Errorf("description = %q", d.description)
	}
	if d.duration != 822 {
		t.Errorf("duration = %d, want 822", d.duration)
	}
	wantDate := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
	if !d.date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", d.date, wantDate)
	}
	if d.producer != "Masturbowanie" {
		t.Errorf("producer = %q", d.producer)
	}
	if d.resolution != "3840x2160" {
		t.Errorf("resolution = %q", d.resolution)
	}
	if d.views != 5191 {
		t.Errorf("views = %d", d.views)
	}
	if d.price != 9 {
		t.Errorf("price = %d", d.price)
	}
	if len(d.categories) != 3 || d.categories[0] != "4K" || d.categories[1] != "MILF" {
		t.Errorf("categories = %v", d.categories)
	}
	if len(d.performers) != 1 || d.performers[0] != "Judyta" {
		t.Errorf("performers = %v", d.performers)
	}
}

func TestParseDetailPageMultipleActors(t *testing.T) {
	body := []byte(`
<h1 class="arrow">Test Scene</h1>
<article><p class="padding10">Description here.</p></article>
<table>
<tr><td>Duration:</td><td>00:25:10</td></tr>
<tr><td>Add date:</td><td>2026-01-15</td></tr>
<tr><td>Actors:</td><td><ul>
<li><a href="aktor,anna,1.html">Anna</a></li>
<li><a href="aktor,basia,2.html">Basia</a></li>
<li><a href="aktor,czeslaw,3.html">Czesław</a></li>
</ul></td></tr>
</table>`)

	d := parseDetailPage(body)
	if len(d.performers) != 3 || d.performers[2] != "Czesław" {
		t.Errorf("performers = %v", d.performers)
	}
	if d.duration != 1510 {
		t.Errorf("duration = %d, want 1510", d.duration)
	}
}

func TestParseDetailPageEmpty(t *testing.T) {
	d := parseDetailPage([]byte(`<div>nothing here</div>`))
	if d.title != "" || d.description != "" || len(d.categories) != 0 {
		t.Errorf("expected empty detail, got %+v", d)
	}
}

const listingTpl = `%s
<div class="flex-video-wrap">%s</div>
<ul class="pagination">%s</ul>`

const itemTpl = `<article class="videoCatalogCard">
<div>
<div class="pictureWrap videoPreview">
<a href="epizod,%d,scene-%d.html" style="width:412px;max-width:100%%;">
<img src="/static/uploaded/video/%d/slider_org.jpg" alt="Scene %d" width="412" height="165">
</a>
</div>
<div class="description">
<h2><a href="epizod,%d,scene-%d.html">Scene %d</a></h2>
</div>
</div>
</div>`

const detailTpl = `
<h1 class="arrow">Test Scene Title</h1>
<article><p class="padding10">Test description.</p></article>
<table>
<tr><td>Price:</td><td><span class="price">10 pts</span></td></tr>
<tr><td>Resolution:</td><td>1920x1080</td></tr>
<tr><td>Duration:</td><td>00:20:00</td></tr>
<tr><td>Add date:</td><td>2026-01-15</td></tr>
</table>
<div class="details_hidden"><table>
<tr><td>Views:</td><td>1000</td></tr>
<tr><td>Producer:</td><td><a class="producerLink" href="produkcja,xes-pl,1.html">Xes</a></td></tr>
<tr><td>Categories:</td><td><ul><li><a href="filtr,4K.html">4K</a></li></ul></td></tr>
<tr><td>Actors:</td><td><ul><li><a href="aktor,test,1.html">Test Model</a></li></ul></td></tr>
</table></div>`

func buildListingPage(ids []int, paginationHTML string) []byte {
	var items string
	for _, id := range ids {
		items += fmt.Sprintf(itemTpl, id, id, id, id, id, id, id)
	}
	return []byte(fmt.Sprintf(listingTpl, "", items, paginationHTML))
}

func newTestServer(pages [][]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")

		switch {
		case r.URL.Path == "/katalog_filmow,1.html":
			pagination := ""
			for p := 1; p <= len(pages); p++ {
				pagination += fmt.Sprintf(`<li><a href="katalog_filmow,%d.html">%d</a></li>`, p, p)
			}
			_, _ = w.Write(buildListingPage(pages[0], pagination))

		case strings.HasPrefix(r.URL.Path, "/katalog_filmow,"):
			var pageNum int
			_, _ = fmt.Sscanf(r.URL.Path, "/katalog_filmow,%d.html", &pageNum)
			idx := pageNum - 1
			if idx >= 0 && idx < len(pages) {
				_, _ = w.Write(buildListingPage(pages[idx], ""))
			} else {
				_, _ = fmt.Fprint(w, `<div>empty</div>`)
			}

		case r.URL.Path == "/aktor,test-model,1,1.html":
			pagination := `<li><a href="aktor,test-model,1,1.html">1</a></li>`
			_, _ = w.Write(buildListingPage(pages[0], pagination))

		default:
			_, _ = fmt.Fprint(w, detailTpl)
		}
	}))
}

func TestListScenes(t *testing.T) {
	ts := newTestServer([][]int{{100, 200}})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/katalog_filmow,1.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
}

func TestListScenesPagination(t *testing.T) {
	page1 := make([]int, 18)
	for i := range page1 {
		page1[i] = i + 1
	}
	page2 := []int{19, 20, 21}

	ts := newTestServer([][]int{page1, page2})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/katalog_filmow,1.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 21 {
		t.Fatalf("got %d scenes, want 21", len(results))
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	ts := newTestServer([][]int{{1, 2, 3, 4}})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/katalog_filmow,1.html", scraper.ListOpts{
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
		t.Fatalf("got %d scenes, want 2", len(results))
	}
}

func TestListScenesPerformerPage(t *testing.T) {
	ts := newTestServer([][]int{{10, 20, 30}})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/aktor,test-model,1,1.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 3 {
		t.Fatalf("got %d scenes, want 3", len(results))
	}
}
