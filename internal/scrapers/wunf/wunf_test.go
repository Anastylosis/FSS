package wunf

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
		{"https://www.wakeupnfuck.com/", true},
		{"https://wakeupnfuck.com/", true},
		{"https://www.wakeupnfuck.com/scene/test_123", true},
		{"https://www.wakeupnfuck.com/actor/test_123", true},
		{"https://www.woodmancastingx.com/", false},
		{"https://www.example.com/", false},
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
		{"/scene/kenza-del-cairo-wunf-441_41390", "41390"},
		{"/scene/test_123.html", "123"},
		{"/scene/no-id-here", ""},
	}
	for _, c := range cases {
		if got := extractID(c.path); got != c.want {
			t.Errorf("extractID(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestTitleCase(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"KENZA DEL CAIRO", "Kenza Del Cairo"},
		{"SCARLETT SPARK", "Scarlett Spark"},
		{"already Title", "Already Title"},
	}
	for _, c := range cases {
		if got := titleCase(c.input); got != c.want {
			t.Errorf("titleCase(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

const testListingHTML = `<html><body>
<a href="/scene/kenza-del-cairo-wunf-441_41390" class="scene item light_background">
    <div class="quality"><span>4K</span></div>
    <div class="picture">
        <p class="timer">1:10:55</p>
        <picture><img src="https://cdn.example.com/41390_thumb.webp" alt="scene"/></picture>
    </div>
    <div class="informations">
        <h3>WUNF 441</h3>
        <p class="sub">Kenza Del Cairo</p>
        <p class="timer">1:10:55</p>
    </div>
</a>
<a href="/scene/alya-stark-wunf-440_41380" class="scene item light_background">
    <div class="picture">
        <p class="timer">55:30</p>
        <picture><img src="https://cdn.example.com/41380_thumb.webp" alt="scene"/></picture>
    </div>
    <div class="informations">
        <h3>WUNF 440</h3>
        <p class="sub">Alya Stark</p>
        <p class="timer">55:30</p>
    </div>
</a>
</body></html>`

// A card whose href carries no numeric id is dropped from the results but must
// still be counted: the raw card count is what the end-of-listing test reads,
// and measuring the filtered slice turned one bad slug into a silent stop.
func TestParseListingPageCountsCardsItCannotParse(t *testing.T) {
	html := testListingHTML[:len(testListingHTML)-len("</body></html>")] +
		`<a href="/scene/no-id-here" class="scene item light_background">
    <div class="informations"><h3>Broken</h3></div>
</a></body></html>`

	scenes, found := parseListingPage([]byte(html), "https://www.wakeupnfuck.com")
	if len(scenes) != 2 {
		t.Errorf("got %d scenes, want 2", len(scenes))
	}
	if found != 3 {
		t.Errorf("found = %d, want 3 — the unparseable card still occupied a slot", found)
	}
}

func TestParseListingPage(t *testing.T) {
	scenes, _ := parseListingPage([]byte(testListingHTML), "https://www.wakeupnfuck.com")

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s1 := scenes[0]
	if s1.id != "41390" {
		t.Errorf("scene 0 id = %q", s1.id)
	}
	if s1.title != "WUNF 441" {
		t.Errorf("scene 0 title = %q", s1.title)
	}
	if s1.performer != "Kenza Del Cairo" {
		t.Errorf("scene 0 performer = %q", s1.performer)
	}
	if s1.duration != 4255 {
		t.Errorf("scene 0 duration = %d, want 4255", s1.duration)
	}
	if s1.thumb != "https://cdn.example.com/41390_thumb.webp" {
		t.Errorf("scene 0 thumb = %q", s1.thumb)
	}

	s2 := scenes[1]
	if s2.id != "41380" {
		t.Errorf("scene 1 id = %q", s2.id)
	}
	if s2.duration != 3330 {
		t.Errorf("scene 1 duration = %d, want 3330", s2.duration)
	}
}

const testDetailHTML = `<html><body>
<h2>Kenza Del Cairo - Wunf 441</h2>
<div class="description">
    Length : 1:10:55<br/>
    Publish Date : 17 May 2026<br/>
</div>
<div class="starring">
    <a class="item" href="/actor/kenza-del-cairo_10498">
        <div class="informations"><p>KENZA DEL CAIRO</p></div>
    </a>
</div>
<div class="tags">
    <ul>
        <li><a href="/tag/adorable_243">Adorable</a></li>
        <li><a href="/tag/anal_93">Anal</a></li>
        <li><a href="/tag/blowjob_41">Blowjob</a></li>
    </ul>
</div>
</body></html>`

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage([]byte(testDetailHTML))

	if d.title != "Kenza Del Cairo - Wunf 441" {
		t.Errorf("title = %q", d.title)
	}
	wantDate := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)
	if !d.date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", d.date, wantDate)
	}
	if d.duration != 4255 {
		t.Errorf("duration = %d, want 4255", d.duration)
	}
	if len(d.tags) != 3 || d.tags[0] != "Adorable" {
		t.Errorf("tags = %v", d.tags)
	}
	if len(d.performers) != 1 || d.performers[0] != "Kenza Del Cairo" {
		t.Errorf("performers = %v", d.performers)
	}
}

// Actor-page markup, reproduced as the site actually emits it. The `<h3>` in the
// first card is deliberately left **unclosed**, running straight into the next
// `<p>` — that is what the live page does, and the earlier hand-tidied fixture
// (which closed the tag) is why this parser passed offline while every scene
// scraped from a real actor page came back with an empty Title.
const testActorHTML = `<html><body>
<h1>KENZA DEL CAIRO</h1>
<p class="sub actor_infos"><strong>Nationnality : French</strong></p>
<div class="list">
    <a href="/scene/kenza-del-cairo-wunf-441_41390" class="scene item">
        <div class="picture">
            <p class="timer">1:10:55</p>
            <img src="https://cdn.example.com/41390_thumb.jpg" alt="player"/>
        </div>
        <div class="informations">
            <h3>Kenza Del Cairo - Wunf 441                            <p>0/5</p>
            <p>2026-05-17 22:09:00&nbsp;</p>
        </div>
    </a>
    <a href="https://www.woodmancastingx.com/casting-x/kenza-del-cairo_41300.html" class="scene item">
        <div class="picture">
            <p class="timer">1:30:00</p>
            <img src="https://cdn.example.com/41300_thumb.jpg" alt="player"/>
        </div>
        <div class="informations">
            <h3>Kenza Del Cairo casting</h3>
        </div>
    </a>
</div>
</body></html>`

func TestParseActorPage(t *testing.T) {
	name, scenes := parseActorPage([]byte(testActorHTML), "https://www.wakeupnfuck.com")

	if name != "Kenza Del Cairo" {
		t.Errorf("actor name = %q", name)
	}

	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1 (cross-site WoodmanCastingX should be skipped)", len(scenes))
	}

	if scenes[0].id != "41390" || scenes[0].title != "Kenza Del Cairo - Wunf 441" {
		t.Errorf("scene: id=%q title=%q", scenes[0].id, scenes[0].title)
	}
	if scenes[0].date.IsZero() {
		t.Error("expected date to be parsed from actor page")
	}
}

func TestRunListing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/scene":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, testListingHTML)
		case "/scene/kenza-del-cairo-wunf-441_41390":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, testDetailHTML)
		case "/scene/alya-stark-wunf-440_41380":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body>
<h2>Alya Stark - Wunf 440</h2>
<div class="description">
    Length : 55:30<br/>
    Publish Date : 14 May 2026<br/>
</div>
<div class="starring">
    <a class="item" href="/actor/alya-stark_10490">
        <div class="informations"><p>ALYA STARK</p></div>
    </a>
</div>
<div class="tags">
    <ul><li><a href="/tag/cute_1">Cute</a></li></ul>
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

func TestRunActor(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/actor/kenza-del-cairo_10498":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, testActorHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), base: ts.URL}
	ctx := context.Background()
	out := make(chan scraper.SceneResult, 20)

	go func() {
		s.runActor(ctx, ts.URL+"/actor/kenza-del-cairo_10498", scraper.ListOpts{}, out)
		close(out)
	}()

	var scenes []string
	for r := range out {
		if r.Kind == scraper.KindScene {
			scenes = append(scenes, r.Scene.Title)
			if len(r.Scene.Performers) != 1 || r.Scene.Performers[0] != "Kenza Del Cairo" {
				t.Errorf("performers = %v", r.Scene.Performers)
			}
		}
	}

	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1: %v", len(scenes), scenes)
	}
}

// Both templates must parse: the main listing closes its <h3>, actor pages do
// not. One lenient pattern covers both, and this pins that rather than leaving
// it to whichever fixture happens to exist.
func TestTitleH3ParsesClosedAndUnclosedTags(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  string
	}{
		{"closed, as the main listing emits it", `<div><h3>WUNF 444</h3><p class="sub">x</p></div>`, "WUNF 444"},
		{"unclosed, as actor pages emit it", `<div><h3>Dido Angel - WUNF 3      <p>0/5</p></div>`, "Dido Angel - WUNF 3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := titleH3Re.FindStringSubmatch(c.block)
			if m == nil {
				t.Fatal("no match — a scene parsed from this template gets an empty Title")
			}
			if got := strings.TrimSpace(m[1]); got != c.want {
				t.Errorf("title = %q, want %q", got, c.want)
			}
		})
	}
}
