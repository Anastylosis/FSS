package woodmanfilms

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.woodmanfilms.com/", true},
		{"https://woodmanfilms.com/", true},
		{"https://www.woodmanfilms.com/scene/test_123", true},
		{"https://www.woodmanfilms.com/pornstar/test_123", true},
		{"https://www.woodmancastingx.com/", false},
		{"https://www.wakeupnfuck.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestExtractID(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/scene/sex-therapy-2-scene-6_8144", "8144"},
		{"/scene/test_123.html", "123"},
		{"/scene/no-id-here", ""},
	}
	for _, c := range cases {
		if got := extractID(c.path); got != c.want {
			t.Errorf("extractID(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestParseDurationText(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"14min", 840},
		{"2 Hour 10min", 7800},
		{"1 Hour 15min", 4500},
		{"45min", 2700},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseDurationText(c.input); got != c.want {
			t.Errorf("parseDurationText(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestTitleCase(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"NATASHA STORM", "Natasha Storm"},
		{"storm", "Storm"},
	}
	for _, c := range cases {
		if got := titleCase(c.input); got != c.want {
			t.Errorf("titleCase(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

const testListingHTML = `<html><body>
<div class="block_960_item">
    <!--[if gt IE 7]><!--><a href="/scene/sex-therapy-2-scene-6_8144" ><!--<![endif]-->
    <div class="item_title">SEX THERAPY 2</div>
    <div class="item_image">
        <img src="https://cdn.example.com/8144_thumb.jpg" alt="Sex Therapy 2"/>
    </div>
    <div class="item_infos">
        <h3>Sex Therapy 2   Scene 6</h3>
        Length : 14min<br>
        Casting : Storm
    </div>
    </a>
</div>
<div class="block_960_item">
    <!--[if gt IE 7]><!--><a href="/scene/russians-3-scene-1_8100" ><!--<![endif]-->
    <div class="item_title">RUSSIANS 3</div>
    <div class="item_image">
        <img src="https://cdn.example.com/8100_thumb.jpg" alt="Russians 3"/>
    </div>
    <div class="item_infos">
        <h3>Russians 3   Scene 1</h3>
        Length : 2 Hour 10min<br>
        Casting : Ivana Sugar, Lola Taylor
    </div>
    </a>
</div>
</body></html>`

func TestParseListingPage(t *testing.T) {
	scenes := parseListingPage([]byte(testListingHTML), "https://www.woodmanfilms.com")

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s1 := scenes[0]
	if s1.id != "8144" {
		t.Errorf("scene 0 id = %q", s1.id)
	}
	if s1.title != "Sex Therapy 2   Scene 6" {
		t.Errorf("scene 0 title = %q", s1.title)
	}
	if s1.series != "SEX THERAPY 2" {
		t.Errorf("scene 0 series = %q", s1.series)
	}
	if len(s1.performers) != 1 || s1.performers[0] != "Storm" {
		t.Errorf("scene 0 performers = %v", s1.performers)
	}
	if s1.duration != 840 {
		t.Errorf("scene 0 duration = %d, want 840", s1.duration)
	}

	s2 := scenes[1]
	if s2.id != "8100" {
		t.Errorf("scene 1 id = %q", s2.id)
	}
	if s2.duration != 7800 {
		t.Errorf("scene 1 duration = %d, want 7800", s2.duration)
	}
	// The Casting line names the whole cast on one line; storing it verbatim
	// filed a two-hander as a single performer called "Ivana Sugar, Lola Taylor".
	if len(s2.performers) != 2 || s2.performers[0] != "Ivana Sugar" || s2.performers[1] != "Lola Taylor" {
		t.Errorf("scene 1 performers = %v", s2.performers)
	}
}

const testDetailHTML = `<html><body>
<span class="scene_title">SEX THERAPY 2 - Scene 6</span>
<p class="info_line info_center">
    <span class="label_info">Length</span> : <span class="yellow">14 minutes</span>
</p>
<div class="movie_infos">
    <label>Description :</label> SEX THERAPY 2 full movie
    <label>Length :</label> 2 Hour 10min
    <label>Directed by :</label> Pierre Woodman
</div>
<a href="/pornstar/natasha-storm_1500">
    <h3>NATASHA STORM</h3>
    <img src="https://cdn.example.com/1500_avatar.jpg" alt="NATASHA STORM"/>
</a>
</body></html>`

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage([]byte(testDetailHTML))

	if d.title != "SEX THERAPY 2 - Scene 6" {
		t.Errorf("title = %q", d.title)
	}
	if d.duration != 840 {
		t.Errorf("duration = %d, want 840", d.duration)
	}
	if len(d.performers) != 1 || d.performers[0] != "Natasha Storm" {
		t.Errorf("performers = %v", d.performers)
	}
	if d.description != "SEX THERAPY 2 full movie" {
		t.Errorf("description = %q", d.description)
	}
}

func TestParseMaxPage(t *testing.T) {
	html := `<a href="/scene?page=18" class="item last">Last</a>`
	got := parseMaxPage([]byte(html))
	if got != 18 {
		t.Errorf("parseMaxPage = %d, want 18", got)
	}
}

func TestRunListing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/scene":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, testListingHTML)
		case "/scene/sex-therapy-2-scene-6_8144":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, testDetailHTML)
		case "/scene/russians-3-scene-1_8100":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body>
<span class="scene_title">Russians 3 - Scene 1</span>
<p class="info_line"><span class="label_info">Length</span> : <span class="yellow">2 hours 10 minutes</span></p>
<a href="/pornstar/ivana-sugar_900"><h3>IVANA SUGAR</h3></a>
<a href="/pornstar/lola-taylor_901"><h3>LOLA TAYLOR</h3></a>
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

// A stored scene mid-page is no reason to discard the rest of a page that has
// already been fetched and parsed. Everything else on it is still emitted; only
// the *next* page request is skipped, which is where the early stop saves
// anything.
func TestRunPornstarKnownIDsFinishesThePage(t *testing.T) {
	var pages int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pornstar/storm_900" {
			pages++
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, "<html><body><h1>STORM</h1>"+testListingHTML+"</body></html>")
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), base: ts.URL}
	out := make(chan scraper.SceneResult, 20)
	go func() {
		s.runPornstar(context.Background(), ts.URL+"/pornstar/storm_900",
			scraper.ListOpts{KnownIDs: map[string]bool{"8144": true}}, out)
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
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}

	if len(ids) != 1 || ids[0] != "8100" {
		t.Fatalf("got %v, want the scene behind the stored 8144", ids)
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if pages != 1 {
		t.Errorf("fetched %d pages, want 1 — the stop must skip the next request", pages)
	}
}

func TestSplitCast(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Storm", []string{"Storm"}},
		{"Ivana Sugar, Lola Taylor", []string{"Ivana Sugar", "Lola Taylor"}},
		{"  Jane  ,  jane ,  ", []string{"Jane"}},
		{"", nil},
	}
	for _, c := range cases {
		got := splitCast(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitCast(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("splitCast(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}
