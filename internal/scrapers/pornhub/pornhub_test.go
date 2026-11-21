package pornhub

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// ---- fixtures ----

type testItem struct {
	vkey       string
	title      string
	thumbURL   string
	durStr     string
	studioSlug string
	studioName string
}

func videoListHTML(items []testItem) []byte {
	var sb strings.Builder
	sb.WriteString(`<html><body><ul class="videos" id="mostRecentVideosSection">`)
	for _, item := range items {
		fmt.Fprintf(&sb, `
<li class="pcVideoListItem js-pop videoblock videoBox" id="v%s"
    data-video-id="%s" data-video-vkey="%s">
    <div class="wrap">
        <div class="phimage">
            <a href="/view_video.php?viewkey=%s" title="%s" class="fade">
                <img src="%s" alt="%s" loading="lazy">
                <div class="marker-overlays js-noFade">
                    <var class="duration">%s</var>
                </div>
            </a>
        </div>
        <div class="thumbnail-info-wrapper clearfix">
            <div class="thumbnail-info">
                <div class="videoUploaderBlock clearfix">
                    <div class="usernameWrap">
                        <a href="/pornstar/%s">%s</a>
                    </div>
                </div>
                <var class="added">2 years ago</var>
            </div>
        </div>
    </div>
</li>`, item.vkey, item.vkey, item.vkey,
			item.vkey, item.title,
			item.thumbURL, item.title,
			item.durStr,
			item.studioSlug, item.studioName)
	}
	sb.WriteString("</ul></body></html>")
	return []byte(sb.String())
}

func testItem1() testItem {
	return testItem{
		vkey:       "aabbcc112233",
		title:      "Scene One",
		thumbURL:   "https://ei.phncdn.com/videos/202305/16/431685661/original/(m=eafTGgaaaa)11.jpg",
		durStr:     "20:47",
		studioSlug: "dee-williams",
		studioName: "Dee Williams",
	}
}

func testItem2() testItem {
	return testItem{
		vkey:       "ddeeff445566",
		title:      "Scene Two &amp; More",
		thumbURL:   "https://ei.phncdn.com/videos/202301/10/123456789/original/(m=eafTGgaaaa)11.jpg",
		durStr:     "10:30",
		studioSlug: "dee-williams",
		studioName: "Dee Williams",
	}
}

func testItem3() testItem {
	return testItem{
		vkey:       "112233aabbcc",
		title:      "Scene Three",
		thumbURL:   "https://ei.phncdn.com/videos/202212/01/987654321/original/(m=eafTGgaaaa)11.jpg",
		durStr:     "1:05:00",
		studioSlug: "dee-williams",
		studioName: "Dee Williams",
	}
}

// testItem4 stands in for a card on the 404 page's recommendations rail — a
// real-looking item that belongs to no listing page and must never be stored.
func testItem4() testItem {
	return testItem{
		vkey:       "998877ffeedd",
		title:      "Recommended Elsewhere",
		thumbURL:   "https://ei.phncdn.com/videos/202410/05/555555555/original/(m=eafTGgaaaa)11.jpg",
		durStr:     "08:15",
		studioSlug: "someone-else",
		studioName: "Someone Else",
	}
}

// ---- TestMatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.pornhub.com/pornstar/dee-williams", true},
		{"https://pornhub.com/pornstar/bettie-bondage", true},
		{"https://www.pornhub.com/channels/mylf", true},
		{"https://www.pornhub.com/pornstar/dee-williams/videos", true},
		{"https://www.pornhub.com/model/alina-rai", true},
		{"https://nl.pornhub.com/model/coconey", true},
		{"https://es.pornhub.com/model/adolfxnika/about", true},
		{"https://fr.pornhub.com/pornstar/valentina-rose/about", true},
		{"https://de.pornhub.com/channels/scott-ward", true},
		{"https://fil.pornhub.com/model/alina-rai", true},
		{"https://pornhub.org/model/alisa_bandicoot", true},
		{"https://www.pornhub.org/pornstar/daisy-taylor", true},
		{"https://www.pornhub.com/users/jecknjill", false},
		{"https://www.pornhub.com.example.com/model/alina-rai", false},
		{"https://notpornhub.com/model/alina-rai", false},
		{"https://www.pornhub.com/view_video.php?viewkey=abc123", false},
		{"https://www.manyvids.com/pornstar/someone", false},
		{"", false},
	}
	for _, c := range cases {
		got := s.MatchesURL(c.url)
		if got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// ---- TestBuildPageURL ----

func TestBuildPageURL(t *testing.T) {
	cases := []struct {
		input   string
		page    int
		want    string
		wantErr bool
	}{
		{
			"https://www.pornhub.com/pornstar/dee-williams",
			1,
			"https://www.pornhub.com/pornstar/dee-williams/videos?page=1",
			false,
		},
		{
			"https://www.pornhub.com/channels/mylf",
			3,
			"https://www.pornhub.com/channels/mylf/videos?page=3",
			false,
		},
		{
			"https://www.pornhub.com/pornstar/dee-williams/videos",
			2,
			"https://www.pornhub.com/pornstar/dee-williams/videos?page=2",
			false,
		},
		{
			"https://www.pornhub.com/model/alina-rai",
			2,
			"https://www.pornhub.com/model/alina-rai/videos?page=2",
			false,
		},
		{
			"https://nl.pornhub.com/model/coconey",
			1,
			"https://www.pornhub.com/model/coconey/videos?page=1",
			false,
		},
		{
			"https://es.pornhub.com/model/adolfxnika/about",
			1,
			"https://www.pornhub.com/model/adolfxnika/videos?page=1",
			false,
		},
		{
			"https://de.pornhub.com/channels/scott-ward",
			2,
			"https://www.pornhub.com/channels/scott-ward/videos?page=2",
			false,
		},
		{
			"http://pornhub.org/model/alisa_bandicoot",
			1,
			"https://www.pornhub.com/model/alisa_bandicoot/videos?page=1",
			false,
		},
		{
			"https://www.pornhub.org/pornstar/daisy-taylor",
			1,
			"https://www.pornhub.com/pornstar/daisy-taylor/videos?page=1",
			false,
		},
		{
			"https://www.manyvids.com/Profile/123",
			1,
			"",
			true,
		},
	}
	for _, c := range cases {
		got, err := buildPageURL(c.input, c.page)
		if (err != nil) != c.wantErr {
			t.Errorf("buildPageURL(%q, %d) error = %v, wantErr %v", c.input, c.page, err, c.wantErr)
			continue
		}
		if got != c.want {
			t.Errorf("buildPageURL(%q, %d) = %q, want %q", c.input, c.page, got, c.want)
		}
	}
}

// ---- TestParseItemsRealPages ----

func TestParseItemsRealPages(t *testing.T) {
	cases := []struct {
		file string
		want []string
	}{
		{"model_videos.html", []string{"ph62c71a9dd4642", "ph62bea82a68e03", "ph629c64c9c5a9b"}},
		{"pornstar_videos.html", []string{"6ab7acefd60c8", "6ab5351e9865e", "6a963cdaa1e0e", "6a88f3df1a3f6"}},
		{"channel_videos.html", []string{"6a85efbf94ba0", "6a73b3fb87800", "6a73b4a3c09cb"}},
	}
	for _, c := range cases {
		body, err := os.ReadFile("testdata/" + c.file)
		if err != nil {
			t.Fatal(err)
		}
		items := parseItems(body)
		var got []string
		for _, it := range items {
			got = append(got, it.vkey)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, want %v", c.file, got, c.want)
		}
	}
}

func TestParseItemsModelPage(t *testing.T) {
	body, err := os.ReadFile("testdata/model_videos.html")
	if err != nil {
		t.Fatal(err)
	}
	items := parseItems(body)
	if len(items) == 0 {
		t.Fatal("no items parsed")
	}
	it := items[0]
	if it.title != "Vaginal gift of fate STEPMOM stuck under the bed." {
		t.Errorf("title = %q", it.title)
	}
	if it.duration != 664 {
		t.Errorf("duration = %d, want 664", it.duration)
	}
	if want := time.Date(2022, 7, 7, 0, 0, 0, 0, time.UTC); !it.date.Equal(want) {
		t.Errorf("date = %v, want %v", it.date, want)
	}
	if it.studio != "Alina Rai" {
		t.Errorf("studio = %q, want Alina Rai", it.studio)
	}
	if !strings.HasPrefix(it.thumbnail, "https://ei.phncdn.com/videos/202207/07/") {
		t.Errorf("thumbnail = %q", it.thumbnail)
	}
}

func TestParseItemsChannelPageNamesTheChannel(t *testing.T) {
	body, err := os.ReadFile("testdata/channel_videos.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range parseItems(body) {
		if it.studio != "MYLF" {
			t.Errorf("%s: studio = %q, want MYLF", it.vkey, it.studio)
		}
	}
}

func TestListScenesModel(t *testing.T) {
	body, err := os.ReadFile("testdata/model_videos.html")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Query().Get("page") != "1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/model/alina-rai/about", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	var ids []string
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if len(ids) != 3 {
		t.Errorf("got %d scenes, want 3: %v", len(ids), ids)
	}
	if len(paths) == 0 || paths[0] != "/model/alina-rai/videos?page=1" {
		t.Errorf("requested %v, want /model/alina-rai/videos?page=1 first", paths)
	}
}

func TestListScenesFollowsModelPromotedToPornstar(t *testing.T) {
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch {
		case r.URL.Path == "/model/khalamite/videos":
			http.Redirect(w, r, "/pornstar/khalamite", http.StatusMovedPermanently)
		case r.URL.Path == "/pornstar/khalamite":
			_, _ = w.Write([]byte("<html><body></body></html>"))
		case r.URL.Path == "/pornstar/khalamite/videos" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write(videoListHTML([]testItem{testItem1(), testItem2()}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/model/khalamite", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	var ids []string
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if len(ids) != 2 {
		t.Errorf("got %d scenes, want 2: %v", len(ids), ids)
	}
	want := []string{"/model/khalamite/videos", "/pornstar/khalamite", "/pornstar/khalamite/videos", "/pornstar/khalamite/videos"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("requested %v, want %v", paths, want)
	}
}

func TestListScenesRemovedModelIsAnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pornstars" {
			_, _ = w.Write(videoListHTML([]testItem{testItem4()}))
			return
		}
		http.Redirect(w, r, "/pornstars", http.StatusMovedPermanently)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/model/too-twisted-taboo", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	var scenes int
	var errs []error
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	if scenes != 0 {
		t.Errorf("stored %d scenes from the /pornstars index", scenes)
	}
	if len(errs) == 0 {
		t.Fatal("a redirect to the /pornstars index reported no error")
	}
	if k := scraper.Classify(errs[0]); !k.MissingData() {
		t.Errorf("classified as %v, which does not count as missing data", k)
	}
}

// ---- TestParseDuration ----

func TestParseDuration(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"20:47", 1247},
		{"10:30", 630},
		{"1:05:00", 3900},
		{"00:45", 45},
		{"", 0},
	}
	for _, c := range cases {
		got := parseutil.ParseDurationColon(c.input)
		if got != c.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

// ---- TestParseItem ----

func TestParseItem(t *testing.T) {
	item := testItem1()
	body := videoListHTML([]testItem{item})
	items := parseItems(body)
	if len(items) != 1 {
		t.Fatalf("parseItems returned %d items, want 1", len(items))
	}
	got := items[0]

	if got.vkey != "aabbcc112233" {
		t.Errorf("vkey = %q, want %q", got.vkey, "aabbcc112233")
	}
	if got.title != "Scene One" {
		t.Errorf("title = %q, want %q", got.title, "Scene One")
	}
	if got.thumbnail != item.thumbURL {
		t.Errorf("thumbnail = %q, want %q", got.thumbnail, item.thumbURL)
	}
	if got.duration != 1247 {
		t.Errorf("duration = %d, want 1247 (20:47)", got.duration)
	}
	wantDate := time.Date(2023, 5, 16, 0, 0, 0, 0, time.UTC)
	if !got.date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", got.date, wantDate)
	}
	if got.studio != "Dee Williams" {
		t.Errorf("studio = %q, want %q", got.studio, "Dee Williams")
	}
}

func TestParseItemHTMLEntities(t *testing.T) {
	item := testItem2()
	body := videoListHTML([]testItem{item})
	items := parseItems(body)
	if len(items) != 1 {
		t.Fatalf("parseItems returned %d items, want 1", len(items))
	}
	if items[0].title != "Scene Two & More" {
		t.Errorf("title = %q, want %q", items[0].title, "Scene Two & More")
	}
}

func TestParseItemHourDuration(t *testing.T) {
	item := testItem3()
	body := videoListHTML([]testItem{item})
	items := parseItems(body)
	if len(items) != 1 {
		t.Fatalf("parseItems returned %d items, want 1", len(items))
	}
	if items[0].duration != 3900 {
		t.Errorf("duration = %d, want 3900 (1:05:00)", items[0].duration)
	}
}

// ---- TestToScene ----

func TestToScene(t *testing.T) {
	item := phItem{
		vkey:      "aabbcc112233",
		title:     "Scene One",
		thumbnail: "https://ei.phncdn.com/videos/202305/16/431685661/original/(m=eafTGgaaaa)11.jpg",
		duration:  1247,
		date:      time.Date(2023, 5, 16, 0, 0, 0, 0, time.UTC),
		studio:    "Dee Williams",
	}
	now := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	scene := toScene("https://www.pornhub.com/pornstar/dee-williams", item, now)

	if scene.ID != "aabbcc112233" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "pornhub" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.URL != "https://www.pornhub.com/view_video.php?viewkey=aabbcc112233" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Duration != 1247 {
		t.Errorf("Duration = %d, want 1247", scene.Duration)
	}
	if len(scene.PriceHistory) != 1 || !scene.PriceHistory[0].IsFree {
		t.Errorf("PriceHistory = %v, want one free snapshot", scene.PriceHistory)
	}
}

// ---- TestListScenes (pornstar) ----

func TestListScenes(t *testing.T) {
	page1 := []testItem{testItem1(), testItem2()}
	page2 := []testItem{testItem3()}

	page := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		switch page {
		case 1:
			_, _ = w.Write(videoListHTML(page1))
		case 2:
			_, _ = w.Write(videoListHTML(page2))
		default:
			_, _ = w.Write(videoListHTML(nil))
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	studioURL := ts.URL + "/pornstar/dee-williams"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	got := map[string]string{}
	for r := range ch {
		if r.Err != nil {
			t.Errorf("unexpected error: %v", r.Err)
			continue
		}
		got[r.Scene.ID] = r.Scene.Title
	}

	if len(got) != 3 {
		t.Fatalf("got %d scenes, want 3: %v", len(got), got)
	}
	want := map[string]string{
		"aabbcc112233": "Scene One",
		"ddeeff445566": "Scene Two & More",
		"112233aabbcc": "Scene Three",
	}
	for id, title := range want {
		if got[id] != title {
			t.Errorf("scene %s title = %q, want %q", id, got[id], title)
		}
	}
}

// ---- TestListScenesChannel ----

func TestListScenesChannel(t *testing.T) {
	items := []testItem{testItem1(), testItem2()}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "page=2") {
			_, _ = w.Write(videoListHTML(nil))
		} else {
			_, _ = w.Write(videoListHTML(items))
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/channels/mylf", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var count int
	for r := range ch {
		if r.Err != nil {
			t.Errorf("unexpected error: %v", r.Err)
		} else {
			count++
		}
	}
	if count != 2 {
		t.Errorf("got %d scenes, want 2", count)
	}
}

// ---- TestListScenesKnownIDs ----

// The listing is not newest-first — riley-reid's page 1 runs 2026-07-23,
// 07-21, 04-03, 03-18, 02-07, 03-30 — so an early stop at the first stored id
// would drop everything after it, and `--full`'s authoritative Save would then
// delete those scenes. KnownIDs is therefore ignored and every run re-walks.
func TestKnownIDsDoNotStopTheWalk(t *testing.T) {
	items := []testItem{testItem1(), testItem2(), testItem3()}

	pageCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageCount++
		if pageCount > 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(videoListHTML(items))
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/pornstar/dee-williams", scraper.ListOpts{
		// The second of three — a truncating stop would leave exactly one.
		KnownIDs: map[string]bool{"ddeeff445566": true},
	})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var ids []string
	sawStoppedEarly := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindStoppedEarly:
			sawStoppedEarly = true
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if len(ids) != 3 {
		t.Errorf("got %d scenes (%v), want all 3 — the walk was truncated", len(ids), ids)
	}
	if sawStoppedEarly {
		t.Error("StoppedEarly was reported; this listing has no usable early stop")
	}
}

// The page number is set on top of whatever the operator wrote, not instead of
// it: rebuilding the query from scratch silently dropped `?o=mr`-style sort and
// filter options from the URL that was actually asked for.
func TestBuildPageURLKeepsTheOperatorsQuery(t *testing.T) {
	cases := []struct {
		in       string
		page     int
		wantPath string
		wantQ    map[string]string
	}{
		{"https://www.pornhub.com/pornstar/dee-williams?o=mr", 2,
			"/pornstar/dee-williams/videos", map[string]string{"o": "mr", "page": "2"}},
		{"https://www.pornhub.com/channels/brazzers?hd=1&o=mr", 3,
			"/channels/brazzers/videos", map[string]string{"hd": "1", "o": "mr", "page": "3"}},
		{"https://www.pornhub.com/pornstar/dee-williams", 1,
			"/pornstar/dee-williams/videos", map[string]string{"page": "1"}},
		// An existing page parameter is replaced, not duplicated.
		{"https://www.pornhub.com/pornstar/dee-williams/videos?page=9", 4,
			"/pornstar/dee-williams/videos", map[string]string{"page": "4"}},
	}
	for _, c := range cases {
		got, err := buildPageURL(c.in, c.page)
		if err != nil {
			t.Errorf("buildPageURL(%q): %v", c.in, err)
			continue
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Errorf("parsing %q: %v", got, err)
			continue
		}
		if u.Path != c.wantPath {
			t.Errorf("buildPageURL(%q) path = %q, want %q", c.in, u.Path, c.wantPath)
		}
		q := u.Query()
		if len(q) != len(c.wantQ) {
			t.Errorf("buildPageURL(%q) query = %v, want %v", c.in, q, c.wantQ)
		}
		for k, v := range c.wantQ {
			if q.Get(k) != v {
				t.Errorf("buildPageURL(%q) %s = %q, want %q", c.in, k, q.Get(k), v)
			}
		}
	}
}

// Past the last page the listing 404s, and that is the only end-of-list signal
// the markup offers — `page_next` is rendered on the final page too. Treating
// it as an error made every full run report a failure and demoted the
// traversal to non-authoritative.
func TestPastEndPage404EndsTheWalkQuietly(t *testing.T) {
	pages := [][]testItem{
		{testItem1(), testItem2()},
		{testItem3()},
	}
	var requested []int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := strconv.Atoi(r.URL.Query().Get("page"))
		requested = append(requested, p)
		if p < 1 || p > len(pages) {
			// The live 404 body carries a recommendations rail whose cards
			// parse as scenes; serving it here pins that they stay out.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(videoListHTML([]testItem{testItem4()}))
			return
		}
		_, _ = w.Write(videoListHTML(pages[p-1]))
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/pornstar/dee-williams", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var ids []string
	var errs []error
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}

	if len(errs) != 0 {
		t.Errorf("a past-end 404 was reported as an error: %v", errs)
	}
	if len(ids) != 3 {
		t.Fatalf("got %d scenes, want 3: %v", len(ids), ids)
	}
	for _, id := range ids {
		if id == "998877ffeedd" {
			t.Error("a card from the 404 recommendations rail was stored as a scene")
		}
	}
	if len(requested) != 3 || requested[2] != 3 {
		t.Errorf("requested pages %v, want 1,2,3 (the 404 detecting the end)", requested)
	}
}

// A 404 on page 1 is a bad slug or a removed performer, not the end of a
// listing, and must stay loud.
func TestFirstPage404IsStillAnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(videoListHTML(nil))
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/pornstar/nobody", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var errs []error
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs = append(errs, r.Err)
		}
	}
	if len(errs) == 0 {
		t.Fatal("a 404 on page 1 reported no error")
	}
	if k := scraper.Classify(errs[0]); !k.MissingData() {
		t.Errorf("classified as %v, which does not count as missing data", k)
	}
}
