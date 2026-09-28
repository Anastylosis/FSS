package woodman

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

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.woodmancastingx.com/", true},
		{"https://woodmancastingx.com/", true},
		{"https://www.woodmancastingx.com/new", true},
		{"https://www.woodmancastingx.com/girl/scarlett-spark_10518", true},
		{"https://www.wakeupnfuck.com/", false},
		{"https://www.example.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestGirlURLParsing(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.woodmancastingx.com/girl/scarlett-spark_10518", "scarlett-spark_10518"},
		{"https://www.woodmancastingx.com/girl/abbie-cat_3606", "abbie-cat_3606"},
		{"https://www.woodmancastingx.com/new", ""},
		{"https://www.woodmancastingx.com/", ""},
	}
	for _, c := range cases {
		m := girlRe.FindStringSubmatch(c.url)
		got := ""
		if m != nil {
			got = m[1]
		}
		if got != c.want {
			t.Errorf("girlRe(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestExtractID(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"/casting-x/scarlett-spark_41270.html", "41270"},
		{"/casting-x/mary-wet_8192.html", "8192"},
		{"https://www.woodmancastingx.com/casting-x/sally-hunter_41498.html", "41498"},
		{"/casting-x/mathilde-ramos-xxxx-slap-me-master-9_41486.html", "41486"},
		{"/no-id-here.html", ""},
	}
	for _, c := range cases {
		if got := extractID(c.url); got != c.want {
			t.Errorf("extractID(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestParseListingDate(t *testing.T) {
	cases := []struct {
		input string
		want  time.Time
	}{
		{"June 6th, 2026", time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)},
		{"June 3rd, 2026", time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)},
		{"June 1st, 2026", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		{"May 22nd, 2026", time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)},
		{"December 25th, 2025", time.Date(2025, 12, 25, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		got := parseListingDate(c.input)
		if !got.Equal(c.want) {
			t.Errorf("parseListingDate(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestParseDurationText(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"1 H 15 mn", 4500},
		{"31 mn", 1860},
		{"20 mn", 1200},
		{"1 H 19 mn", 4740},
		{"2 H 5 mn", 7500},
		{"31:35", 1895},
		{"1:19:38", 4778},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseDurationText(c.input); got != c.want {
			t.Errorf("parseDurationText(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestParseDetailDuration(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"1 hour 20 minutes", 4800},
		{"41 minutes", 2460},
		{"2 hours 5 minutes", 7500},
		{"1hour 20 min", 4800},
	}
	for _, c := range cases {
		if got := parseDetailDuration(c.input); got != c.want {
			t.Errorf("parseDetailDuration(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestTitleCase(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"SCARLETT SPARK", "Scarlett Spark"},
		{"MARY WET", "Mary Wet"},
		{"abbie cat", "Abbie Cat"},
		{"Pierre Woodman", "Pierre Woodman"},
	}
	for _, c := range cases {
		if got := titleCase(c.input); got != c.want {
			t.Errorf("titleCase(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

const testListingHTML = `<html><body>
<div id="updates" class="scenes_list flat_content designV3">
<div class="day items container_1">
  <div class="even day_title">
      June 6th, 2026          </div>
            <div class="element">
    <a class="item scene " href="/casting-x/mary-wet_8192.html" title="Mary Wet">
        <img class="thumb" src="https://cdn.example.com/8192_thumb.jpg" alt="Mary Wet"  />
        <img src="/images/cache/144254/4K-bg-gold.png" class="icon_4k">
    </a>
      <p class="name"><a href="/casting-x/mary-wet_8192.html">Mary Wet &nbsp; * UPDATED *</a></p>
      <p class="info">MARY WET - CASTING X  - Thomas Stone - Pierre Woodman</p>
      <p class="details">1 H 15 mn</p>
      <p class="language">English</p>
      <div class="clear"></div>
    </div>
        </div>
<div class="day items container_1">
  <div class="even day_title">
      June 3rd, 2026          </div>
            <div class="element">
    <a class="item scene " href="/casting-x/sally-hunter_41498.html" title="Sally Hunter">
        <img class="thumb" src="https://cdn.example.com/41498_thumb.jpg" alt="Sally Hunter"  />
        <img src="/images/cache/144254/4K-bg-gold.png" class="icon_4k">
    </a>
      <p class="name"><a href="/casting-x/sally-hunter_41498.html">Sally Hunter</a></p>
      <p class="info">Casting fully updated later</p>
      <p class="details">31 mn</p>
      <p class="language">Original sound</p>
      <div class="clear"></div>
    </div>
            <div class="element">
    <a class="item scene " href="https://www.wakeupnfuck.com/scene/kenza-del-cairo-wunf-441_41390" title="Kenza Del Cairo - Wunf 441">
        <img class="thumb" src="https://cdn.example.com/41390_thumb.jpg" alt="Kenza" />
    </a>
      <p class="name"><a href="https://www.wakeupnfuck.com/scene/kenza-del-cairo-wunf-441_41390">Kenza Del Cairo - Wunf 441</a></p>
      <p class="details">45 mn</p>
      <div class="clear"></div>
    </div>
        </div>
</div>
<div class="pagerf">
    <div class="pagination">
    <div class="first_prev"></div>
    <div class="pages_container">
    <a href="/new" class="active">1</a><a href="/new?page=2" class="">2</a><a href="/new?page=3" class="">3</a>
    <a>...</a>
    <a href="/new?page=361">361</a>
    </div>
    <div class="next_last">
              <a href="/new?page=361">Last</a>
                    <a href="/new?page=2">Next</a>
          </div>
  </div>
</div>
</body></html>`

func TestParseListingPage(t *testing.T) {
	scenes := parseListingPage([]byte(testListingHTML), "https://www.woodmancastingx.com")

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (WUNF link should be skipped)", len(scenes))
	}

	s1 := scenes[0]
	if s1.id != "8192" {
		t.Errorf("scene 0 id = %q, want 8192", s1.id)
	}
	if s1.title != "Mary Wet" {
		t.Errorf("scene 0 title = %q, want 'Mary Wet'", s1.title)
	}
	if s1.thumb != "https://cdn.example.com/8192_thumb.jpg" {
		t.Errorf("scene 0 thumb = %q", s1.thumb)
	}
	if s1.duration != 4500 {
		t.Errorf("scene 0 duration = %d, want 4500", s1.duration)
	}
	if !s1.date.Equal(time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("scene 0 date = %v", s1.date)
	}
	wantURL := "https://www.woodmancastingx.com/casting-x/mary-wet_8192.html"
	if s1.url != wantURL {
		t.Errorf("scene 0 url = %q, want %q", s1.url, wantURL)
	}

	s2 := scenes[1]
	if s2.id != "41498" {
		t.Errorf("scene 1 id = %q, want 41498", s2.id)
	}
	if s2.title != "Sally Hunter" {
		t.Errorf("scene 1 title = %q", s2.title)
	}
	if s2.duration != 1860 {
		t.Errorf("scene 1 duration = %d, want 1860", s2.duration)
	}
}

func TestParseMaxPage(t *testing.T) {
	got := parseMaxPage([]byte(testListingHTML))
	if got != 361 {
		t.Errorf("parseMaxPage = %d, want 361", got)
	}
}

const testDetailHTML = `<html><body>
<h1 class="full_length">Scarlett Spark casting</h1>
<div class="video_infos">
<div class="pannel_info">
<p class="info_line info_center">
    <span class="label_info">Published</span> : 2026-04-23
</p>
<p class="info_line info_center">
    <span class="label_info">Length</span> : <span class="yellow">1 hour 20 minutes</span>
</p>
</div>
</div>
<p class="description">
    A romanian girl, Scarlett Spark has an audition with Pierre Woodman.
    <span>She will answer general questions about her life.</span>
    <span>Then Scarlett Spark will undress to show her body naked.</span>
</p>
<div class="tags">
    <a href="/keywords/adorable%2C243" class="tag">Adorable</a>
    <a href="/keywords/anal%2C93" class="tag">Anal</a>
    <a href="/keywords/beautiful%2C3" class="tag">Beautiful</a>
    <a href="/keywords" class="tag more_tag">More Tags ...</a>
</div>
<div class="block_girls_videos items">
    <h2 class="casting">Casting</h2>
    <a class="girl_item" href="/girl/scarlett-spark_10518">
        <span class="name">SCARLETT SPARK</span>
        <img src="https://cdn.example.com/10518_avatar.jpg"/>
        <div class="clear"></div>
    </a>
</div>
</body></html>`

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage([]byte(testDetailHTML), "https://www.woodmancastingx.com")

	if d.title != "Scarlett Spark casting" {
		t.Errorf("title = %q", d.title)
	}
	if d.date != (time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", d.date)
	}
	if d.duration != 4800 {
		t.Errorf("duration = %d, want 4800", d.duration)
	}
	if len(d.tags) != 3 || d.tags[0] != "Adorable" || d.tags[1] != "Anal" || d.tags[2] != "Beautiful" {
		t.Errorf("tags = %v", d.tags)
	}
	if len(d.performers) != 1 || d.performers[0] != "Scarlett Spark" {
		t.Errorf("performers = %v", d.performers)
	}
	if d.description == "" || !containsSubstring(d.description, "romanian girl") {
		t.Errorf("description = %q", d.description)
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && contains(s, sub))
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

const testGirlHTML = `<html><body>
<div id="actorShow" class="designV3">
    <div class="actor">
        <div class="infos">
            <h1>SCARLETT SPARK</h1>
            <p class="nationnality">Romanian</p>
        </div>
    </div>
    <section>
        <div class="videos">
            <div class="page_title">
                <h2>Hardcore <span>videos</span></h2>
            </div>
            <a class="item scene" href="/casting-x/scarlett-spark_41270.html">
                <img class="thumb iefixaimg" src="https://cdn.example.com/41270_thumb.jpg" alt="Casting of SCARLETT SPARK video" height="137" />
                <span class="title">Scarlett Spark</span>
                <span class="infos">1hour 20 min Casting</span>
            </a>
            <a class="item scene" href="/casting-x/scarlett-spark-xxxx_41485.html">
                <img class="thumb iefixaimg" src="https://cdn.example.com/41485_thumb.jpg" alt="Casting" />
                <span class="title">Scarlett Spark - XXXX</span>
                <span class="infos">45 min Hardcore</span>
            </a>
            <a class="item scene" href="https://www.wakeupnfuck.com/scene/scarlett-spark-wunf_41500">
                <img class="thumb iefixaimg" src="https://cdn.example.com/41500_thumb.jpg" alt="WUNF" />
                <span class="title">Scarlett Spark - Wunf</span>
                <span class="infos">30 min WUNF</span>
            </a>
        </div>
    </section>
</div>
</body></html>`

func TestParseGirlPage(t *testing.T) {
	name, scenes := parseGirlPage([]byte(testGirlHTML), "https://www.woodmancastingx.com")

	if name != "Scarlett Spark" {
		t.Errorf("girl name = %q, want 'Scarlett Spark'", name)
	}

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (WUNF should be skipped)", len(scenes))
	}

	if scenes[0].id != "41270" || scenes[0].title != "Scarlett Spark" {
		t.Errorf("scene 0: id=%q title=%q", scenes[0].id, scenes[0].title)
	}
	if scenes[0].duration != 4800 {
		t.Errorf("scene 0 duration = %d, want 4800", scenes[0].duration)
	}

	if scenes[1].id != "41485" || scenes[1].title != "Scarlett Spark - XXXX" {
		t.Errorf("scene 1: id=%q title=%q", scenes[1].id, scenes[1].title)
	}
}

func TestRunListing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/new":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, testListingHTML)
		case "/casting-x/mary-wet_8192.html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, testDetailHTML)
		case "/casting-x/sally-hunter_41498.html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body>
<h1 class="full_length">Sally Hunter casting</h1>
<div class="pannel_info">
<p class="info_line"><span class="label_info">Published</span> : 2026-06-03</p>
<p class="info_line"><span class="label_info">Length</span> : <span class="yellow">31 minutes</span></p>
</div>
<p class="description">Sally Hunter auditions.</p>
<div class="tags"><a href="/keywords/cute%2C1" class="tag">Cute</a></div>
<div class="block_girls_videos items">
<a class="girl_item" href="/girl/sally-hunter_12000">
    <span class="name">SALLY HUNTER</span>
</a>
</div>
</body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), base: ts.URL}
	ctx := context.Background()
	out := make(chan scraper.SceneResult, 20)

	go func() {
		s.runListing(ctx, ts.URL+"/", scraper.ListOpts{}, out)
		close(out)
	}()

	var scenes []string
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene.Title)
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(scenes), scenes)
	}
}

func TestRunGirl(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/girl/scarlett-spark_10518":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, testGirlHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), base: ts.URL}
	ctx := context.Background()
	out := make(chan scraper.SceneResult, 20)

	go func() {
		s.runGirl(ctx, ts.URL+"/girl/scarlett-spark_10518", scraper.ListOpts{}, out)
		close(out)
	}()

	var scenes []string
	for r := range out {
		if r.Kind == scraper.KindScene {
			scenes = append(scenes, r.Scene.Title)
			if len(r.Scene.Performers) != 1 || r.Scene.Performers[0] != "Scarlett Spark" {
				t.Errorf("performers = %v", r.Scene.Performers)
			}
		}
	}

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(scenes), scenes)
	}
}

// The performer page is one already-parsed fetch, so a stored scene in the
// middle of it must be skipped rather than truncating the rest of her
// catalogue.
func TestRunGirlKnownIDsSkipsAndContinues(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/girl/scarlett-spark_10518" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, testGirlHTML)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), base: ts.URL}
	out := make(chan scraper.SceneResult, 20)

	go func() {
		s.runGirl(context.Background(), ts.URL+"/girl/scarlett-spark_10518",
			scraper.ListOpts{KnownIDs: map[string]bool{"41270": true}}, out)
		close(out)
	}()

	var ids []string
	stopped := false
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}

	if len(ids) != 1 || ids[0] != "41485" {
		t.Fatalf("got %v, want the one scene behind the stored 41270", ids)
	}
	if !stopped {
		t.Error("expected StoppedEarly to report that something was skipped")
	}
}

// buildFullListingPage renders a full page of distinct cards, so the walk does
// not end on the "short page" rule.
func buildFullListingPage(n int) string {
	var sb strings.Builder
	sb.WriteString(`<html><body><div id="updates" class="scenes_list flat_content designV3">`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, `<div class="day items container_1"><div class="even day_title">June 6th, 2026</div>
<div class="element">
<a class="item scene " href="/casting-x/model-%d_%d.html" title="Model %d"><img class="thumb" src="https://cdn.example.com/%d_thumb.jpg" alt="Model %d" /></a>
<p class="name"><a href="/casting-x/model-%d_%d.html">Model %d</a></p>
<p class="details">31 mn</p>
<div class="clear"></div>
</div></div>`, i, 9000+i, i, 9000+i, i, i, 9000+i, i)
	}
	sb.WriteString(`</div></body></html>`)
	return sb.String()
}

// The walk ends on a short page, but a CMS that clamps an out-of-range page
// back to a real one serves a full page forever. It must end on a page that
// adds no new id.
func TestRunListingStopsWhenThePagerClamps(t *testing.T) {
	page := buildFullListingPage(pageSize)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/new" {
			_, _ = fmt.Fprint(w, page)
			return
		}
		_, _ = fmt.Fprint(w, testDetailHTML)
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), base: ts.URL}
	out := make(chan scraper.SceneResult, 4*pageSize)
	done := make(chan int)
	go func() {
		s.runListing(context.Background(), ts.URL+"/", scraper.ListOpts{}, out)
		close(out)
	}()
	go func() {
		n := 0
		for r := range out {
			if r.Kind == scraper.KindScene {
				n++
			}
		}
		done <- n
	}()

	select {
	case n := <-done:
		if n != pageSize {
			t.Errorf("got %d scenes, want %d — every page after the first repeats", n, pageSize)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the walk did not terminate against a pager that clamps")
	}
}
