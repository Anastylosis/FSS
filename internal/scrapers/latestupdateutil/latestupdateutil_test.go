package latestupdateutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

// testSite is the Sofie Marie row: the shape this template was first written
// against.
func testSite() SiteConfig {
	return SiteConfig{
		SiteID:     "sofiemarie",
		SiteBase:   "https://sofiemariexxx.com",
		StudioName: "Sofie Marie",
		AltDomains: []string{"yummysofie.com"},
		Patterns:   []string{"sofiemariexxx.com"},
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(testSite())
	yes := []string{
		"https://sofiemariexxx.com",
		"https://sofiemariexxx.com/",
		"https://www.sofiemariexxx.com",
		"https://sofiemariexxx.com/models/sofie-marie.html",
		"https://sofiemariexxx.com/dvds/Dirt-Road-Warriors.html",
		"http://sofiemariexxx.com/categories/movies.html",
		// Alias domain serving the same catalogue.
		"https://yummysofie.com",
		"https://yummysofie.com/",
		"https://www.yummysofie.com/models/sofie-marie.html",
	}
	no := []string{
		"https://example.com",
		"https://sofiemarie.com",
		"https://notsofiemariexxx.com",
		"https://notyummysofie.com",
		// The press blog, not a catalogue — it must stay unclaimed.
		"https://www.sofiemarie.com",
	}

	for _, u := range yes {
		if !s.MatchesURL(u) {
			t.Errorf("expected match for %s", u)
		}
	}
	for _, u := range no {
		if s.MatchesURL(u) {
			t.Errorf("unexpected match for %s", u)
		}
	}
}

func TestClassifyURL(t *testing.T) {
	cases := []struct {
		url  string
		want urlKind
	}{
		{"https://sofiemariexxx.com", kindUpdates},
		{"https://sofiemariexxx.com/", kindUpdates},
		{"https://sofiemariexxx.com/categories/movies.html", kindUpdates},
		{"https://sofiemariexxx.com/updates/page_1.html", kindUpdates},
		{"https://sofiemariexxx.com/models/sofie-marie.html", kindModel},
		{"https://sofiemariexxx.com/dvds/Dirt-Road-Warriors.html", kindDVD},
	}
	for _, tc := range cases {
		got := classifyURL(tc.url)
		if got != tc.want {
			t.Errorf("classifyURL(%q) = %d, want %d", tc.url, got, tc.want)
		}
	}
}

const testSceneBlock = `<div class="latestUpdateB" data-setid="26443">
<div class="videoPic">
<a href="https://sofiemariexxx.com/scenes/my-scene_vids.html">
<img class="update_thumb thumbs stdimage" src0_1x="/content//contentthumbs/02/33/50233-1x.jpg" src0_2x="/content//contentthumbs/02/33/50233-2x.jpg" src0_3x="/content//contentthumbs/02/33/50233-3x.jpg" src0_4x="/content//contentthumbs/02/33/50233-4x.jpg" cnt="1" v="0" />
</a>
</div>
<div class="latestUpdateBinfo">
<h4 class="link_bright">
<a href="https://sofiemariexxx.com/scenes/my-scene_vids.html">My Test Scene &amp; More</a>
</h4>
<p class="link_light">
<a class="link_bright infolink" href="https://sofiemariexxx.com/models/sofie-marie.html">Sofie Marie</a>
<a class="link_bright infolink" href="https://sofiemariexxx.com/models/michael.html">Michael</a>
</p>
<ul class="videoInfo">
<li class="text_med"><!-- Date -->
04/25/2026</li>
<li class="text_med"><i class="fas fa-camera"></i>53</li>
<li class="text_med"><i class="fas fa-video"></i>44 min</li>
</ul>
</div>
</div>
<div class="latestUpdateB" data-setid="99999">`

func TestParseSceneBlocks(t *testing.T) {
	scenes := parseSceneBlocks([]byte(testSceneBlock))
	if len(scenes) == 0 {
		t.Fatal("expected at least one scene")
	}

	ps := scenes[0]
	if ps.id != "26443" {
		t.Errorf("id = %q, want 26443", ps.id)
	}
	if ps.title != "My Test Scene & More" {
		t.Errorf("title = %q, want %q", ps.title, "My Test Scene & More")
	}
	if ps.relURL != "https://sofiemariexxx.com/scenes/my-scene_vids.html" {
		t.Errorf("relURL = %q", ps.relURL)
	}
	if ps.thumbnail != "/content//contentthumbs/02/33/50233-4x.jpg" {
		t.Errorf("thumbnail = %q", ps.thumbnail)
	}
	if len(ps.performers) != 2 || ps.performers[0] != "Sofie Marie" || ps.performers[1] != "Michael" {
		t.Errorf("performers = %v", ps.performers)
	}
	if ps.date != "04/25/2026" {
		t.Errorf("date = %q", ps.date)
	}
	if ps.duration != "44" {
		t.Errorf("duration = %q", ps.duration)
	}
}

func TestParseSceneBlockNoPerformers(t *testing.T) {
	block := `<div class="latestUpdateB" data-setid="441">
<div class="videoPic">
<a href="/scenes/photo-only_highres.html">
<img src0_1x="/content//contentthumbs/67/29/6729-1x.jpg" />
</a>
</div>
<div class="latestUpdateBinfo">
<h4 class="link_bright">
<a href="/scenes/photo-only_highres.html">Photo Set Title</a>
</h4>
<p class="link_light"></p>
<ul class="videoInfo">
<li class="text_med"><!-- Date -->
03/15/2026</li>
<li class="text_med"><i class="fas fa-camera"></i>80</li>
</ul>
</div>
</div>`

	scenes := parseSceneBlocks([]byte(block))
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}

	ps := scenes[0]
	if ps.id != "441" {
		t.Errorf("id = %q", ps.id)
	}
	if ps.title != "Photo Set Title" {
		t.Errorf("title = %q", ps.title)
	}
	if len(ps.performers) != 0 {
		t.Errorf("performers = %v", ps.performers)
	}
	if ps.duration != "" {
		t.Errorf("duration = %q, want empty", ps.duration)
	}
	if ps.thumbnail != "/content//contentthumbs/67/29/6729-1x.jpg" {
		t.Errorf("thumbnail = %q (should fall back to src0_1x)", ps.thumbnail)
	}
}

func TestToScene(t *testing.T) {
	ps := parsedScene{
		id:         "26443",
		title:      "My Test Scene",
		relURL:     "/scenes/my-scene_vids.html",
		thumbnail:  "/content//contentthumbs/02/33/50233-4x.jpg",
		performers: []string{"Sofie Marie"},
		date:       "04/25/2026",
		duration:   "44",
	}
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	scene := toScene(ps, "https://sofiemariexxx.com", "sofiemarie", "Sofie Marie", "https://sofiemariexxx.com/", now)

	if scene.ID != "26443" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "sofiemarie" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.URL != "https://sofiemariexxx.com/scenes/my-scene_vids.html" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Thumbnail != "https://sofiemariexxx.com/content//contentthumbs/02/33/50233-4x.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if scene.Duration != 2640 {
		t.Errorf("Duration = %d, want 2640", scene.Duration)
	}
	if scene.Studio != "Sofie Marie" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	expected := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(expected) {
		t.Errorf("Date = %v, want %v", scene.Date, expected)
	}
}

func TestToSceneAbsoluteURL(t *testing.T) {
	ps := parsedScene{
		id:        "100",
		title:     "Abs URL Scene",
		relURL:    "https://sofiemariexxx.com/scenes/abs_vids.html",
		thumbnail: "https://cdn.example.com/thumb.jpg",
	}
	now := time.Now().UTC()
	scene := toScene(ps, "https://sofiemariexxx.com", "sofiemarie", "Sofie Marie", "https://sofiemariexxx.com/", now)

	if scene.URL != "https://sofiemariexxx.com/scenes/abs_vids.html" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Thumbnail != "https://cdn.example.com/thumb.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
}

func TestParseDate(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"04/25/2026", "2026-04-25"},
		{"12/31/2025", "2025-12-31"},
		{"", "0001-01-01"},
		{"not-a-date", "0001-01-01"},
	}
	for _, tc := range cases {
		got := parseDate(tc.in)
		if got.Format("2006-01-02") != tc.want {
			t.Errorf("parseDate(%q) = %s, want %s", tc.in, got.Format("2006-01-02"), tc.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"44", 2640},
		{"26", 1560},
		{"0", 0},
		{"", 0},
	}
	for _, tc := range cases {
		got := parseDuration(tc.in)
		if got != tc.want {
			t.Errorf("parseDuration(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestEstimateTotal(t *testing.T) {
	body := []byte(`<div class="pagination ">
<a class="active" href="movies.html">1</a>
<a href="movies_2.html">2</a>
<a href="movies_79.html">79</a>
</div>`)
	got := estimateTotal(body, 12)
	if got != 79*12 {
		t.Errorf("estimateTotal = %d, want %d", got, 79*12)
	}
}

func TestEstimateTotalNoPagination(t *testing.T) {
	body := []byte(`<html><body>no pagination here</body></html>`)
	got := estimateTotal(body, 5)
	if got != 5 {
		t.Errorf("estimateTotal = %d, want 5", got)
	}
}

func TestHasNextPage(t *testing.T) {
	body := []byte(`<div class="pagination ">
<a class="active" href="movies.html">1</a>
<a href="movies_2.html">2</a>
<a href="movies_5.html">5</a>
</div>`)

	if !hasNextPage(body, 1) {
		t.Error("expected hasNextPage(1) = true")
	}
	if !hasNextPage(body, 4) {
		t.Error("expected hasNextPage(4) = true")
	}
	if hasNextPage(body, 5) {
		t.Error("expected hasNextPage(5) = false")
	}
}

func TestExtractModelPagination(t *testing.T) {
	body := []byte(`<div class="pagination ">
<a class="active" href="sets.php?id=2">1</a>
<a href="sets.php?id=2&page=2">2</a>
<a href="sets.php?id=2&page=86">86</a>
</div>`)

	id, maxPage := extractModelPagination(body)
	if id != "2" {
		t.Errorf("modelID = %q, want 2", id)
	}
	if maxPage != 86 {
		t.Errorf("maxPage = %d, want 86", maxPage)
	}
}

func TestExtractModelPaginationNone(t *testing.T) {
	body := []byte(`<html><body>no pagination</body></html>`)
	id, maxPage := extractModelPagination(body)
	if id != "" || maxPage != 0 {
		t.Errorf("expected empty id and 0 max, got %q, %d", id, maxPage)
	}
}

func makeUpdatesPage(scenes int, maxPage int) string {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := 1; i <= scenes; i++ {
		fmt.Fprintf(&b, `<div class="latestUpdateB" data-setid="%d">
<div class="videoPic"><a href="/scenes/scene-%d_vids.html">
<img src0_1x="/thumb-%d.jpg" /></a></div>
<div class="latestUpdateBinfo">
<h4 class="link_bright"><a href="/scenes/scene-%d_vids.html">Scene %d</a></h4>
<p class="link_light"><a class="link_bright infolink" href="/models/model.html">Model</a></p>
<ul class="videoInfo">
<li class="text_med"><!-- Date -->
04/20/2026</li>
<li class="text_med"><i class="fas fa-video"></i>30 min</li>
</ul></div></div>`, i, i, i, i, i)
	}
	if maxPage > 1 {
		b.WriteString(`<div class="pagination ">`)
		for p := 1; p <= maxPage; p++ {
			if p == 1 {
				fmt.Fprintf(&b, `<a href="movies.html">1</a>`)
			} else {
				fmt.Fprintf(&b, `<a href="movies_%d.html">%d</a>`, p, p)
			}
		}
		b.WriteString(`</div>`)
	}
	b.WriteString("</body></html>")
	return b.String()
}

func TestPaginatedScrape(t *testing.T) {
	page1 := makeUpdatesPage(3, 2)
	page2 := makeUpdatesPage(2, 2)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "movies_2"):
			_, _ = fmt.Fprint(w, page2)
		default:
			_, _ = fmt.Fprint(w, page1)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.runUpdates(context.Background(), ts.URL, scraper.ListOpts{}, out)
	}()

	var scenes int
	var total int
	for r := range out {
		if r.Total > 0 {
			total = r.Total
			continue
		}
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		scenes++
	}

	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
	if scenes != 5 {
		t.Errorf("scenes = %d, want 5", scenes)
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	page := makeUpdatesPage(5, 3)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, page)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.runUpdates(context.Background(), ts.URL, scraper.ListOpts{
			KnownIDs: map[string]bool{"3": true},
		}, out)
	}()

	var scenes int
	var stopped bool
	for r := range out {
		if r.Kind == scraper.KindStoppedEarly {
			stopped = true
			continue
		}
		if r.Total > 0 {
			continue
		}
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		scenes++
	}

	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if scenes != 2 {
		t.Errorf("scenes = %d, want 2 (IDs 1 and 2 before known ID 3)", scenes)
	}
}

func TestModelPagination(t *testing.T) {
	modelPage := `<html><body>
<div class="latestUpdateB" data-setid="100">
<div class="videoPic"><a href="/scenes/s1_vids.html">
<img src0_1x="/t1.jpg" /></a></div>
<div class="latestUpdateBinfo">
<h4 class="link_bright"><a href="/scenes/s1_vids.html">Scene One</a></h4>
<p class="link_light"></p>
<ul class="videoInfo"><li class="text_med"><!-- Date -->
01/01/2026</li></ul></div></div>
<div class="pagination ">
<a class="active" href="sets.php?id=7">1</a>
<a href="sets.php?id=7&page=2">2</a>
</div>
</body></html>`

	modelPage2 := `<html><body>
<div class="latestUpdateB" data-setid="200">
<div class="videoPic"><a href="/scenes/s2_vids.html">
<img src0_1x="/t2.jpg" /></a></div>
<div class="latestUpdateBinfo">
<h4 class="link_bright"><a href="/scenes/s2_vids.html">Scene Two</a></h4>
<p class="link_light"></p>
<ul class="videoInfo"><li class="text_med"><!-- Date -->
02/01/2026</li></ul></div></div>
</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "page=2") {
			_, _ = fmt.Fprint(w, modelPage2)
		} else {
			_, _ = fmt.Fprint(w, modelPage)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	studioURL := ts.URL + "/models/test-model.html"
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.runModel(context.Background(), studioURL, scraper.ListOpts{}, out)
	}()

	var scenes int
	var total int
	for r := range out {
		if r.Total > 0 {
			total = r.Total
			continue
		}
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		scenes++
	}

	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if scenes != 2 {
		t.Errorf("scenes = %d, want 2", scenes)
	}
}

func TestDVDScrape(t *testing.T) {
	dvdPage := `<html><body>
<div class="latestUpdateB" data-setid="500">
<div class="videoPic"><a href="/scenes/dvd-scene_vids.html">
<img src0_1x="/dvd-t.jpg" /></a></div>
<div class="latestUpdateBinfo">
<h4 class="link_bright"><a href="/scenes/dvd-scene_vids.html">DVD Scene</a></h4>
<p class="link_light">
<a class="link_bright infolink" href="/models/star.html">Star</a>
</p>
<ul class="videoInfo"><li class="text_med"><!-- Date -->
06/15/2025</li>
<li class="text_med"><i class="fas fa-video"></i>60 min</li>
</ul></div></div>
</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, dvdPage)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	studioURL := ts.URL + "/dvds/Test-DVD.html"
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.runDVD(context.Background(), studioURL, scraper.ListOpts{}, out)
	}()

	var scenes int
	var total int
	for r := range out {
		if r.Total > 0 {
			total = r.Total
			continue
		}
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		scenes++
		if r.Scene.Title != "DVD Scene" {
			t.Errorf("Title = %q", r.Scene.Title)
		}
		if r.Scene.Duration != 3600 {
			t.Errorf("Duration = %d, want 3600", r.Scene.Duration)
		}
		if len(r.Scene.Performers) != 1 || r.Scene.Performers[0] != "Star" {
			t.Errorf("Performers = %v", r.Scene.Performers)
		}
	}

	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if scenes != 1 {
		t.Errorf("scenes = %d, want 1", scenes)
	}
}

func TestScraperInterface(t *testing.T) {
	var _ scraper.StudioScraper = New(testSite())
}

// The alias must resolve to the one canonical catalogue. Fetching yummysofie.com
// on its own would ingest every scene a second time under a second set of URLs.
func TestAliasDomainUsesTheCanonicalSiteBase(t *testing.T) {
	s := New(testSite())
	if s.siteBase != testSite().SiteBase {
		t.Fatalf("siteBase = %q, want %q", s.siteBase, testSite().SiteBase)
	}
	if strings.Contains(s.siteBase, "yummysofie") {
		t.Errorf("siteBase %q points at the alias host", s.siteBase)
	}
	if s.ID() != "sofiemarie" {
		t.Errorf("ID() = %q — the alias must not introduce a second SiteID", s.ID())
	}
}
