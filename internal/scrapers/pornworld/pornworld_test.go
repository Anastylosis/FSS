package pornworld

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const fixtureListing = `<html><body>
<div class="pagination-items row">
<article class="card scene">
  <a class="thumbnail-pic js-inline-video"
     href="//pornworld.com/watch/4625222-44821/hot-blonde-gets-pounded"
     data-video-src="https://cdn77-video.gtflixtv.com/preview.mp4">
    <img class="card-img img-fluid" data-src="https://cdn77-image.gtflixtv.com/thumb1.jpg" alt="Hot Blonde Gets Pounded" />
    <div class="release-date">2026 May, 07</div>
  </a>
  <div class="card-footer">
    <p class="card-title text-nowrap">
      <a href="//pornworld.com/watch/4625222-44821/hot-blonde-gets-pounded" title="Hot Blonde Gets Pounded">Hot Blonde Gets Pounded</a>
    </p>
    <div class="d-flex justify-content-between">
      <div class="starring text-truncate-scroll">
        <span>
          <a href="//pornworld.com/model/123/jane-doe" title="Jane Doe">Jane Doe</a>
          &amp; <a href="//pornworld.com/model/456/john-smith" title="John Smith">John Smith</a>
        </span>
      </div>
      <div class="info">
        <span class="video-duration"><i class="bi bi-clock pe-1"></i>39:40</span>
      </div>
    </div>
  </div>
</article>
<article class="card scene">
  <a class="thumbnail-pic js-inline-video"
     href="//pornworld.com/watch/4625100-44820/brunette-milf-threesome"
     data-video-src="https://cdn77-video.gtflixtv.com/preview2.mp4">
    <img class="card-img img-fluid" data-src="https://cdn77-image.gtflixtv.com/thumb2.jpg" alt="Brunette MILF Threesome" />
    <div class="release-date">2026 May, 06</div>
  </a>
  <div class="card-footer">
    <p class="card-title text-nowrap">
      <a href="//pornworld.com/watch/4625100-44820/brunette-milf-threesome" title="Brunette MILF Threesome">Brunette MILF Threesome</a>
    </p>
    <div class="d-flex justify-content-between">
      <div class="starring text-truncate-scroll">
        <span>
          <a href="//pornworld.com/model/789/lisa-ann" title="Lisa Ann">Lisa Ann</a>
        </span>
      </div>
      <div class="info">
        <span class="video-duration"><i class="bi bi-clock pe-1"></i>1:02:15</span>
      </div>
    </div>
  </div>
</article>
</div>
</body></html>`

const fixtureDetail = `<html><head>
<script type="application/ld+json">
{
  "@type": "VideoObject",
  "name": "Hot Blonde Gets Pounded",
  "description": "A steamy scene with a hot blonde.",
  "thumbnailUrl": "https://cdn77-image.gtflixtv.com/detail1.jpg",
  "uploadDate": "2026-05-07T00:00:00+02:00",
  "datePublished": "2026-05-07",
  "duration": "PT39M40S"
}
</script>
</head><body>
<div class="scene-details row">
<h1 class="text-primary scene__title">Hot Blonde Gets Pounded</h1>
<div>
<strong>Tags:</strong>
<a href="/videos?tags=blonde" class="link-secondary">Blonde</a>,
<a href="/videos?tags=hardcore" class="link-secondary">Hardcore</a>,
<a href="/videos?tags=cumshot" class="link-secondary">Cumshot</a>
</div>
</div>
</body></html>`

const fixtureEmpty = `<html><body>
<div class="pagination-items row">
</div>
</body></html>`

func TestParseListing(t *testing.T) {
	entries := parseListing([]byte(fixtureListing))
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	e := entries[0]
	if e.id != "4625222-44821" {
		t.Errorf("id = %q, want %q", e.id, "4625222-44821")
	}
	if e.title != "Hot Blonde Gets Pounded" {
		t.Errorf("title = %q", e.title)
	}
	if e.date.Year() != 2026 || e.date.Month() != 5 || e.date.Day() != 7 {
		t.Errorf("date = %v", e.date)
	}
	if e.duration != "39:40" {
		t.Errorf("duration = %q", e.duration)
	}
	if len(e.performers) != 2 || e.performers[0] != "Jane Doe" || e.performers[1] != "John Smith" {
		t.Errorf("performers = %v", e.performers)
	}
	if e.thumbnail != "https://cdn77-image.gtflixtv.com/thumb1.jpg" {
		t.Errorf("thumbnail = %q", e.thumbnail)
	}

	e2 := entries[1]
	if e2.id != "4625100-44820" {
		t.Errorf("entry[1].id = %q", e2.id)
	}
	if e2.duration != "1:02:15" {
		t.Errorf("entry[1].duration = %q", e2.duration)
	}
	if len(e2.performers) != 1 || e2.performers[0] != "Lisa Ann" {
		t.Errorf("entry[1].performers = %v", e2.performers)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"39:40", 2380},
		{"1:02:15", 3735},
		{"0:30", 30},
		{"", 0},
	}
	for _, tc := range tests {
		got := parseutil.ParseDurationColon(tc.input)
		if got != tc.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/videos":
			page := r.URL.Query().Get("page")
			if page == "1" || page == "" {
				_, _ = fmt.Fprint(w, fixtureListing)
			} else {
				_, _ = fmt.Fprint(w, fixtureEmpty)
			}
		default:
			_, _ = fmt.Fprint(w, fixtureDetail)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.Duration == 0 {
				t.Errorf("scene %s: duration = 0", r.Scene.ID)
			}
			if r.Scene.Description != "A steamy scene with a hot blonde." {
				t.Errorf("scene %s: description = %q", r.Scene.ID, r.Scene.Description)
			}
			if len(r.Scene.Tags) != 3 {
				t.Errorf("scene %s: got %d tags, want 3", r.Scene.ID, len(r.Scene.Tags))
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, fixtureListing)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"4625222-44821": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotStoppedEarly bool
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			t.Error("should not have received a scene")
		case scraper.KindStoppedEarly:
			gotStoppedEarly = true
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if !gotStoppedEarly {
		t.Error("expected StoppedEarly")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://pornworld.com/videos", true},
		{"https://www.pornworld.com/watch/123-456/scene", true},
		{"https://ddfnetwork.com/", true},
		{"https://1by-day.com/", true},
		{"https://ddfbusty.com/", true},
		{"https://handsonhardcore.com/", true},
		{"https://hotlegsandfeet.com/", true},
		{"https://houseoftaboo.com/", true},
		{"https://onlyblowjob.com/", true},
		{"https://bustyworld.com/", true},
		{"https://eurogirlsongirls.com/", true},
		{"https://other-site.com/", false},
	}
	for _, tc := range tests {
		got := s.MatchesURL(tc.url)
		if got != tc.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

// The listing renders the full month name. Parsed as the three-letter
// abbreviation, every date outside May, June and July came back zero — eleven
// months a year of undated scenes. Live format: "2026 September, 15".
func TestParseCardDate(t *testing.T) {
	cases := map[string]string{
		"2026 September, 15":   "2026-09-15",
		" 2026 September, 08 ": "2026-09-08",
		"2026 September, 8":    "2026-09-08",
		"2026 Sep, 15":         "2026-09-15",
		"2026 May, 01":         "2026-05-01",
	}
	for raw, want := range cases {
		got := parseCardDate(raw)
		if got.IsZero() {
			t.Errorf("parseCardDate(%q) = zero, want %s", raw, want)
			continue
		}
		if got.Format("2006-01-02") != want {
			t.Errorf("parseCardDate(%q) = %s, want %s", raw, got.Format("2006-01-02"), want)
		}
	}
	if !parseCardDate("not a date").IsZero() {
		t.Error("an unparseable date must stay zero")
	}
}
