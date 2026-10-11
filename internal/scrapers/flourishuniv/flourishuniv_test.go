package flourishuniv

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
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.flourishuniv.com/episodes/", true},
		{"https://flourishuniv.com/", true},
		{"https://tour.theflourishxxx.com/", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestParseCast(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"Maya Woulfe, Margarita Lopez, Isiah Maxwell, Tommy Pistol", []string{"Maya Woulfe", "Margarita Lopez", "Isiah Maxwell", "Tommy Pistol"}},
		{"Gia Derza, Isiah Maxwell, with Annie Archer", []string{"Gia Derza", "Isiah Maxwell", "Annie Archer"}},
		{"Hannah Grace, Queen Rose Freya, and Brick Cummings", []string{"Hannah Grace", "Queen Rose Freya", "Brick Cummings"}},
		{"Katie Kinz, James Angel, with Spicy Jayde and Brick Cummings", []string{"Katie Kinz", "James Angel", "Spicy Jayde and Brick Cummings"}},
	}
	for _, tt := range tests {
		got := parseCast(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("parseCast(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parseCast(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

func TestParseEpisodes(t *testing.T) {
	html := `<div class="episodes-grid">
		<div class="episode-card">
			<div class="ep-thumb">
				<img src="https://example.com/thumb1.jpg" alt="Pilot"/>
				<div class="ep-num">E1</div>
			</div>
			<div class="ep-body">
				<h3>Pilot: Welcome to Flourish U</h3>
				<div class="ep-desc"><p>First episode description.</p>
<p>Starring: Alice, Bob, with Charlie</p>
</div>
				<div class="ep-actions">
					<a href="https://tour.theflourishxxx.com/trailers/Flourish-Ep-01.html" class="btn btn-primary">Watch Now</a>
				</div>
			</div>
		</div>
		<div class="episode-card">
			<div class="ep-thumb">
				<img src="https://example.com/thumb2.jpg" alt="Episode 2"/>
				<div class="ep-num">E2</div>
			</div>
			<div class="ep-body">
				<h3>Second Episode</h3>
				<div class="ep-desc"><p>Second description.</p>
<p>Starring: Dave, Eve</p>
</div>
				<div class="ep-actions">
					<a href="https://tour.theflourishxxx.com/trailers/Flourish-Ep-02.html" class="btn btn-primary">Watch Now</a>
				</div>
			</div>
		</div>
	</div>`

	eps := parseEpisodes([]byte(html))
	if len(eps) != 2 {
		t.Fatalf("got %d episodes, want 2", len(eps))
	}

	ep := eps[0]
	if ep.slug != "Flourish-Ep-01" {
		t.Errorf("slug = %q, want %q", ep.slug, "Flourish-Ep-01")
	}
	if ep.title != "Pilot: Welcome to Flourish U" {
		t.Errorf("title = %q, want %q", ep.title, "Pilot: Welcome to Flourish U")
	}
	if ep.thumb != "https://example.com/thumb1.jpg" {
		t.Errorf("thumb = %q, want %q", ep.thumb, "https://example.com/thumb1.jpg")
	}
	if ep.epNum != "E1" {
		t.Errorf("epNum = %q, want %q", ep.epNum, "E1")
	}
	if ep.desc != "First episode description." {
		t.Errorf("desc = %q, want %q", ep.desc, "First episode description.")
	}
	if len(ep.performers) != 3 || ep.performers[0] != "Alice" || ep.performers[2] != "Charlie" {
		t.Errorf("performers = %v, want [Alice Bob Charlie]", ep.performers)
	}
}

func TestParseEpisodesNoWatchURL(t *testing.T) {
	html := `<div class="episode-card">
		<div class="ep-body"><h3>No Link</h3>
			<div class="ep-desc"><p>No watch URL.</p></div>
		</div>
	</div>`

	eps := parseEpisodes([]byte(html))
	if len(eps) != 0 {
		t.Errorf("got %d episodes, want 0 (no watch URL)", len(eps))
	}
}

func TestParseDetailPage(t *testing.T) {
	html := `<div class="info">
		<h3>info:</h3>
		<p>
			Added: September 27, 2021<br/>
			Runtime: 7&nbsp;Photos, 01:17:19<br/>
		</p>
	</div>
	<ul class="tags">
		<li><a href="/categories/anal_1_d.html">Anal</a></li>
		<li><a href="/categories/bbc_1_d.html">BBC</a></li>
	</ul>
	<div class="description">
		<h3>description:</h3>
		<p>Detail description text.</p>
	</div>`

	d := parseDetailPage([]byte(html))
	if d.date.Format("2006-01-02") != "2021-09-27" {
		t.Errorf("date = %v, want 2021-09-27", d.date)
	}
	if d.duration != 1*3600+17*60+19 {
		t.Errorf("duration = %d, want %d", d.duration, 1*3600+17*60+19)
	}
	if len(d.tags) != 2 || d.tags[0] != "Anal" || d.tags[1] != "BBC" {
		t.Errorf("tags = %v, want [Anal BBC]", d.tags)
	}
	if d.description != "Detail description text." {
		t.Errorf("description = %q", d.description)
	}
}

func TestParseDetailPageRuntimeNoPhotos(t *testing.T) {
	html := `<p>Added: May 1, 2026<br/>Runtime: 23:42<br/></p>`
	d := parseDetailPage([]byte(html))
	if d.duration != 23*60+42 {
		t.Errorf("duration = %d, want %d", d.duration, 23*60+42)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"23:42", 23*60 + 42},
		{"01:17:19", 1*3600 + 17*60 + 19},
		{"00:46", 46},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseutil.ParseDurationColon(tt.in); got != tt.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestListScenes(t *testing.T) {
	episodesHTML := `<div class="episodes-grid">
		<div class="episode-card">
			<div class="ep-thumb">
				<img src="/thumb.jpg" alt="Test"/>
				<div class="ep-num">E1</div>
			</div>
			<div class="ep-body">
				<h3>Test Episode</h3>
				<div class="ep-desc"><p>A description.</p>
<p>Starring: Alice, Bob</p>
</div>
				<div class="ep-actions">
					<a href="DETAIL_URL/trailers/Test-Ep.html" class="btn btn-primary">Watch Now</a>
				</div>
			</div>
		</div>
	</div>`

	detailHTML := `<div class="info"><p>Added: March 15, 2025<br/>Runtime: 30:00<br/></p></div>
		<ul class="tags"><li><a href="/categories/test_1_d.html">TestTag</a></li></ul>
		<div class="description"><h3>description:</h3><p>Detail desc.</p></div>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/episodes"):
			html := strings.ReplaceAll(episodesHTML, "DETAIL_URL", "http://"+r.Host)
			_, _ = fmt.Fprint(w, html)
		case strings.Contains(r.URL.Path, "/trailers/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New()
	s.base = ts.URL
	s.detailBase = ts.URL

	episodesFixed := strings.ReplaceAll(episodesHTML, "DETAIL_URL", ts.URL)
	_ = episodesFixed

	ch, err := s.ListScenes(context.Background(), ts.URL+"/episodes/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes []scraper.SceneResult
	for r := range ch {
		scenes = append(scenes, r)
	}

	var foundScene bool
	for _, r := range scenes {
		if r.Kind == scraper.KindScene {
			foundScene = true
			if want := ts.URL + "/episodes/"; r.Scene.StudioURL != want {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, want)
			}
			if r.Scene.ID != "Test-Ep" {
				t.Errorf("ID = %q, want %q", r.Scene.ID, "Test-Ep")
			}
			if r.Scene.Title != "Test Episode" {
				t.Errorf("title = %q", r.Scene.Title)
			}
			if r.Scene.Date.Format("2006-01-02") != "2025-03-15" {
				t.Errorf("date = %v, want 2025-03-15", r.Scene.Date)
			}
			if r.Scene.Duration != 30*60 {
				t.Errorf("duration = %d, want %d", r.Scene.Duration, 30*60)
			}
			if len(r.Scene.Tags) != 1 || r.Scene.Tags[0] != "TestTag" {
				t.Errorf("tags = %v", r.Scene.Tags)
			}
			if len(r.Scene.Performers) != 2 {
				t.Errorf("performers = %v", r.Scene.Performers)
			}
			if r.Scene.Series != "Flourish University" {
				t.Errorf("series = %q", r.Scene.Series)
			}
			if r.Scene.Description != "A description." {
				t.Errorf("description = %q", r.Scene.Description)
			}
		}
	}
	if !foundScene {
		t.Error("no scene result found")
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	html := `<div class="episode-card">
		<div class="ep-thumb"><img src="/t.jpg" alt=""/><div class="ep-num">E1</div></div>
		<div class="ep-body"><h3>Ep 1</h3>
			<div class="ep-desc"><p>Desc.</p></div>
			<div class="ep-actions"><a href="https://tour.theflourishxxx.com/trailers/Known-Slug.html" class="btn btn-primary">Watch Now</a></div>
		</div>
	</div>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, html)
	}))
	defer ts.Close()

	s := New()
	s.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL+"/episodes/", scraper.ListOpts{
		KnownIDs: map[string]bool{"Known-Slug": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var stoppedEarly bool
	for r := range ch {
		if r.Kind == scraper.KindStoppedEarly {
			stoppedEarly = true
		}
		if r.Kind == scraper.KindScene {
			t.Error("got scene, expected stop")
		}
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
}

func TestScraperInterface(t *testing.T) {
	s := New()
	var _ scraper.StudioScraper = s
	if s.ID() != "flourishuniv" {
		t.Errorf("ID() = %q", s.ID())
	}
}
