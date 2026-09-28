package flourishutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{SiteID: "theflourishxxx", Domain: "theflourishxxx.com", StudioName: "The Flourish XXX"})
	tests := []struct {
		url  string
		want bool
	}{
		{"https://tour.theflourishxxx.com/categories/movies_1_d.html", true},
		{"https://www.theflourishxxx.com/", true},
		{"https://theflourishxxx.com/", true},
		{"https://tour.theflourishpov.com/", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func buildListingPage(cards []string, maxPage int) string {
	var sb strings.Builder
	sb.WriteString(`<html><body><div class="items clear">`)
	for _, c := range cards {
		sb.WriteString(c)
	}
	sb.WriteString(`</div>`)
	if maxPage > 1 {
		sb.WriteString(`<div class="pagination"><ul>`)
		for i := 1; i <= maxPage; i++ {
			fmt.Fprintf(&sb, `<li><a href="/categories/movies_%d_d.html">%d</a></li>`, i, i)
		}
		sb.WriteString(`</ul></div>`)
	}
	sb.WriteString(`</body></html>`)
	return sb.String()
}

func parentCard(id, slug, title, thumb, duration, date string, performers []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `<div class="item item-video">
	<div class="item-thumb">
		<a href="/trailers/%s.html" title="%s">
			<span>%s</span>
			<img id="set-target-%s" width="480" height="270" alt="%s" class="mainThumb thumbs stdimage" src="%s" />
		</a>
	</div>
	<div class="timeDate">%s <em>|</em>  %s	</div>`, slug, title, title, id, title, thumb, duration, date)
	if len(performers) > 0 {
		sb.WriteString("\n\t<p>\n\t\t&nbsp;Featuring ")
		for i, p := range performers {
			if i > 0 {
				sb.WriteString("\n\t\t\t, ")
			}
			fmt.Fprintf(&sb, `		<a href="/models/%s.html">%s</a>`, strings.ReplaceAll(p, " ", "-"), p)
		}
		sb.WriteString("\n\t\t\t\t</p>")
	}
	sb.WriteString("\n</div>")
	return sb.String()
}

func childCard(id, slug, title, thumb, duration, date string, performers []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `<div class="item item-video">
	<div class="item-thumb">
		<a href="/trailers/%s.html" title="%s">
			<span>%s</span>
			<img id="set-target-%s" width="480" height="270" alt="%s" class="mainThumb thumbs stdimage" src="%s" />
		</a>
	</div>
	<div class="timeDate">%s <em>|</em>  %s <em>|</em>`, slug, title, title, id, title, thumb, duration, date)
	if len(performers) > 0 {
		sb.WriteString("\n\t\t&nbsp;Featuring:  ")
		for i, p := range performers {
			if i > 0 {
				sb.WriteString("\n\t\t\t, ")
			}
			fmt.Fprintf(&sb, `		<a href="/models/%s.html">%s</a>`, strings.ReplaceAll(p, " ", "-"), p)
		}
	}
	sb.WriteString("\n</div>")
	return sb.String()
}

func TestParseListingPageParent(t *testing.T) {
	html := buildListingPage([]string{
		parentCard("2179", "Test-Scene", "Test Scene", "/content/thumb.jpg", "23:42", "2026-05-17", []string{"Alice", "Bob"}),
		parentCard("2177", "Another-Scene", "Another Scene", "/content/thumb2.jpg", "00:46", "2026-05-15", []string{"Charlie"}),
	}, 5)

	items := parseListingPage([]byte(html))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	item := items[0]
	if item.id != "2179" {
		t.Errorf("id = %q, want %q", item.id, "2179")
	}
	if item.title != "Test Scene" {
		t.Errorf("title = %q, want %q", item.title, "Test Scene")
	}
	if item.url != "/trailers/Test-Scene.html" {
		t.Errorf("url = %q, want %q", item.url, "/trailers/Test-Scene.html")
	}
	if item.thumb != "/content/thumb.jpg" {
		t.Errorf("thumb = %q, want %q", item.thumb, "/content/thumb.jpg")
	}
	if item.duration != 23*60+42 {
		t.Errorf("duration = %d, want %d", item.duration, 23*60+42)
	}
	if item.date.Format("2006-01-02") != "2026-05-17" {
		t.Errorf("date = %v, want 2026-05-17", item.date)
	}
	if len(item.performers) != 2 || item.performers[0] != "Alice" || item.performers[1] != "Bob" {
		t.Errorf("performers = %v, want [Alice Bob]", item.performers)
	}
}

func TestParseListingPageChild(t *testing.T) {
	html := buildListingPage([]string{
		childCard("2172", "Test-POV", "Test POV", "/content/pov.jpg", "02:20", "2026-05-11", []string{"MrFlourish", "Syren De Mer"}),
	}, 1)

	items := parseListingPage([]byte(html))
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	item := items[0]
	if item.id != "2172" {
		t.Errorf("id = %q, want %q", item.id, "2172")
	}
	if len(item.performers) != 2 || item.performers[0] != "MrFlourish" || item.performers[1] != "Syren De Mer" {
		t.Errorf("performers = %v, want [MrFlourish Syren De Mer]", item.performers)
	}
}

func TestParseListingPagePhotoCount(t *testing.T) {
	card := `<div class="item item-video">
	<div class="item-thumb">
		<a href="/trailers/Battle-Sex.html" title="Battle Sex">
			<img id="set-target-2171" class="mainThumb thumbs stdimage" src="/content/t.jpg" />
		</a>
	</div>
	<div class="timeDate">40&nbsp;Photos, 26:23 <em>|</em>  2026-05-09	</div>
	<p>&nbsp;Featuring <a href="/models/Alice.html">Alice</a></p>
</div>`

	items := parseListingPage([]byte(card))
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].duration != 26*60+23 {
		t.Errorf("duration = %d, want %d", items[0].duration, 26*60+23)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"23:42", 23*60 + 42},
		{"00:46", 46},
		{"02:20", 2*60 + 20},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseutil.ParseDurationColon(tt.in); got != tt.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestParseDetailPage(t *testing.T) {
	html := `<div class="description">
		<h3>description:</h3>
		<p>First paragraph.<br></br>Second paragraph with <a href="/link">a link</a>.</p>
	</div>
	<ul class="tags">
		<li><a href="/categories/amateur_1_d.html">Amateur</a></li>
		<li><a href="/categories/bbc_1_d.html">BBC</a></li>
		<li><a href="/categories/interracial_1_d.html">Interracial</a></li>
	</ul>`

	d := parseDetailPage([]byte(html))
	if !strings.Contains(d.description, "First paragraph.") {
		t.Errorf("description missing first paragraph: %q", d.description)
	}
	if !strings.Contains(d.description, "Second paragraph") {
		t.Errorf("description missing second paragraph: %q", d.description)
	}
	if strings.Contains(d.description, "<a") {
		t.Errorf("description contains HTML tags: %q", d.description)
	}
	if len(d.tags) != 3 {
		t.Fatalf("got %d tags, want 3", len(d.tags))
	}
	if d.tags[0] != "Amateur" || d.tags[1] != "BBC" || d.tags[2] != "Interracial" {
		t.Errorf("tags = %v", d.tags)
	}
}

func TestParseDetailPageEmpty(t *testing.T) {
	d := parseDetailPage([]byte(`<html><body>no description here</body></html>`))
	if d.description != "" {
		t.Errorf("description = %q, want empty", d.description)
	}
	if len(d.tags) != 0 {
		t.Errorf("tags = %v, want empty", d.tags)
	}
}

func TestEstimateTotal(t *testing.T) {
	html := `<a href="/categories/movies_1_d.html">1</a>
		<a href="/categories/movies_2_d.html">2</a>
		<a href="/categories/movies_86_d.html">86</a>`

	total := estimateTotal([]byte(html))
	if total != 86*perPage {
		t.Errorf("total = %d, want %d", total, 86*perPage)
	}
}

func TestListScenes(t *testing.T) {
	detailHTML := `<div class="description"><h3>description:</h3><p>A test description.</p></div>
		<ul class="tags"><li><a href="/categories/test_1_d.html">TestTag</a></li></ul>`

	card := parentCard("100", "Test-Scene", "Test Scene", "/content/t.jpg", "10:00", "2026-01-15", []string{"Performer One"})
	listHTML := buildListingPage([]string{card}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/categories/movies_"):
			_, _ = fmt.Fprint(w, listHTML)
		case strings.Contains(r.URL.Path, "/trailers/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "testsite", Domain: "example.com", StudioName: "Test Studio"})
	s.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL+"/categories/movies_1_d.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes []scraper.SceneResult
	for r := range ch {
		scenes = append(scenes, r)
	}

	if len(scenes) < 2 {
		t.Fatalf("got %d results, want at least 2 (progress + scene)", len(scenes))
	}

	var foundScene bool
	for _, r := range scenes {
		if r.Kind == scraper.KindScene {
			foundScene = true
			if r.Scene.ID != "100" {
				t.Errorf("scene ID = %q, want %q", r.Scene.ID, "100")
			}
			if r.Scene.Title != "Test Scene" {
				t.Errorf("title = %q, want %q", r.Scene.Title, "Test Scene")
			}
			if r.Scene.Description != "A test description." {
				t.Errorf("description = %q, want %q", r.Scene.Description, "A test description.")
			}
			if len(r.Scene.Tags) != 1 || r.Scene.Tags[0] != "TestTag" {
				t.Errorf("tags = %v, want [TestTag]", r.Scene.Tags)
			}
			if len(r.Scene.Performers) != 1 || r.Scene.Performers[0] != "Performer One" {
				t.Errorf("performers = %v, want [Performer One]", r.Scene.Performers)
			}
			if r.Scene.Duration != 600 {
				t.Errorf("duration = %d, want 600", r.Scene.Duration)
			}
			if r.Scene.Studio != "Test Studio" {
				t.Errorf("studio = %q, want %q", r.Scene.Studio, "Test Studio")
			}
		}
	}
	if !foundScene {
		t.Error("no scene result found")
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	card1 := parentCard("100", "Scene-A", "Scene A", "/t.jpg", "10:00", "2026-01-01", nil)
	card2 := parentCard("200", "Scene-B", "Scene B", "/t2.jpg", "05:00", "2026-01-02", nil)
	listHTML := buildListingPage([]string{card1, card2}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, listHTML)
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "testsite", Domain: "example.com", StudioName: "Test"})
	s.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL+"/categories/movies_1_d.html", scraper.ListOpts{
		KnownIDs: map[string]bool{"100": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var stoppedEarly bool
	var ids []string
	for r := range ch {
		if r.Kind == scraper.KindStoppedEarly {
			stoppedEarly = true
		}
		if r.Kind == scraper.KindScene {
			ids = append(ids, r.Scene.ID)
		}
	}
	// Card 100 is stored; card 200 sits behind it on the same page and must
	// still be emitted before the walk stops.
	if len(ids) != 1 || ids[0] != "200" {
		t.Errorf("scene IDs = %v, want [200]", ids)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
}

func TestScraperInterface(t *testing.T) {
	s := New(SiteConfig{SiteID: "theflourishxxx", Domain: "theflourishxxx.com", StudioName: "The Flourish XXX"})
	var _ scraper.StudioScraper = s

	if s.ID() != "theflourishxxx" {
		t.Errorf("ID() = %q, want %q", s.ID(), "theflourishxxx")
	}
	patterns := s.Patterns()
	if len(patterns) != 2 {
		t.Errorf("Patterns() length = %d, want 2", len(patterns))
	}
}

func TestModelPage(t *testing.T) {
	card := parentCard("500", "Model-Scene", "Model Scene", "/t.jpg", "15:00", "2026-03-01", []string{"Model Name"})
	modelHTML := buildListingPage([]string{card}, 1)
	detailHTML := `<div class="description"><h3>description:</h3><p>Model desc.</p></div><ul class="tags"></ul>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/models/"):
			_, _ = fmt.Fprint(w, modelHTML)
		case strings.Contains(r.URL.Path, "/trailers/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "testsite", Domain: "example.com", StudioName: "Test"})
	s.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL+"/models/Model-Name.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var foundScene bool
	for r := range ch {
		if r.Kind == scraper.KindScene {
			foundScene = true
			if r.Scene.ID != "500" {
				t.Errorf("scene ID = %q, want %q", r.Scene.ID, "500")
			}
		}
	}
	if !foundScene {
		t.Error("no scene result from model page")
	}
}
