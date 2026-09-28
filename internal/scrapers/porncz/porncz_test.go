package porncz

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://porncz.com/", true},
		{"https://www.porncz.com/en/videos", true},
		{"https://www.porncz.com/en/sexy-blonde-can-t-wait-for-his-hard-cock", true},
		{"https://sexintaxi.com/", false},
		{"https://other.com/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"PT18M51S", 18*60 + 51},
		{"PT1H2M3S", 3600 + 120 + 3},
		{"PT30M0S", 1800},
		{"PT0M45S", 45},
		{"PT25M", 1500},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseutil.ParseDurationISO(c.input); got != c.want {
			t.Errorf("ParseDurationISO(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestParseDetailPage(t *testing.T) {
	body := []byte(`<html><head>
<script type="application/ld+json">
{"@context":"https://schema.org","@type":"Organization","name":"PornCZ"}
</script>
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@type": "VideoObject",
  "name": "Sexy blonde cant wait for his hard cock",
  "description": "This cock raising petite blonde with nice tits was ready.",
  "thumbnailUrl": "https://img2.porncz.com/en/media/abc-video_detail.webp",
  "uploadDate": "2026-05-18T00:00:00+01:00",
  "duration": "PT18M51S",
  "actor": [{"@type":"Person","name":"Stanley Johnson"},{"@type":"Person","name":"Jessie Ames"}],
  "partOfSeries": {"@type":"VideoSeries","name":"Teen from bohemia"}
}
</script>
</head></html>`)

	vo := parseutil.ExtractVideoObject(body)
	if vo == nil {
		t.Fatal("expected VideoObject")
	}
	if vo.Name != "Sexy blonde cant wait for his hard cock" {
		t.Errorf("name = %q", vo.Name)
	}
	if vo.Description != "This cock raising petite blonde with nice tits was ready." {
		t.Errorf("desc = %q", vo.Description)
	}
	if vo.ThumbnailURL != "https://img2.porncz.com/en/media/abc-video_detail.webp" {
		t.Errorf("thumb = %q", vo.ThumbnailURL)
	}
	if vo.UploadDate != "2026-05-18T00:00:00+01:00" {
		t.Errorf("upload = %q", vo.UploadDate)
	}
	if vo.Duration != "PT18M51S" {
		t.Errorf("duration = %q", vo.Duration)
	}
	if len(vo.Actors) != 2 || vo.Actors[0] != "Stanley Johnson" || vo.Actors[1] != "Jessie Ames" {
		t.Errorf("actors = %v", vo.Actors)
	}
	if vo.PartOfSeries != "Teen from bohemia" {
		t.Errorf("series = %q", vo.PartOfSeries)
	}
}

func TestParseDetailPageNoVideoObject(t *testing.T) {
	body := []byte(`<html><head>
<script type="application/ld+json">
{"@type":"Organization","name":"PornCZ"}
</script>
</head></html>`)

	vo := parseutil.ExtractVideoObject(body)
	if vo != nil {
		t.Error("expected no VideoObject")
	}
}

func TestParseListingCards(t *testing.T) {
	body := []byte(`<div class="card card--item video-thumbnails"
	data-type="video"
	data-video="https://play.porncz.com/thumbnail/019dc85f-bffa-731f-a628-a5f019643177">
<div class="card__img">
	<img src="/img/loader_img.gif"
		data-src="https://img2.porncz.com/en/media/abc-video_list.webp"
		alt="Sexy blonde can&#039;t wait for his hard cock"
		class="card-img-top img-fluid dmb-6">
	<div class="card__img_badge bottom-right">18:51</div>
</div>
<div class="card-body">
	<div class="dropdown card-dropdown">
		<ul class="dropdown-menu">
			<li><a class="dropdown-item" href="https://www.teenfrombohemia.com/?utm_campaign=video_list_dropdown_menu&amp;utm_source=porncz.com"><i class="icon icon-website me-1 icon--s18"></i>teenfrombohemia.com</a></li>
		</ul>
	</div>
	<a href="/en/sexy-blonde-can-t-wait-for-his-hard-cock" class="card__link stretched-link dmb-1">Sexy blonde can&#039;t wait</a>
</div>
</div></div></div>
<div class="card card--item video-thumbnails"
	data-type="video"
	data-video="https://play.porncz.com/thumbnail/0195c82a-4bed-72a9-9a96-0c60e3117440">
<div class="card__img">
	<img src="/img/loader_img.gif"
		data-src="https://img2.porncz.com/en/media/def-video_list.webp"
		alt="Lusy and Daniel loves it hard"
		class="card-img-top img-fluid dmb-6">
	<div class="card__img_badge bottom-right">24:51</div>
</div>
<div class="card-body">
	<div class="dropdown card-dropdown">
		<ul class="dropdown-menu">
			<li><a class="dropdown-item" href="https://www.amateripremium.com/?utm_campaign=video_list_dropdown_menu&amp;utm_source=porncz.com"><i class="icon icon-website me-1 icon--s18"></i>amateripremium.com</a></li>
		</ul>
	</div>
	<a href="/en/lusy-and-daniel-loves-it-hard" class="card__link stretched-link dmb-1">Lusy and Daniel loves it hard</a>
</div>
</div></div></div>`)

	items := parseListingCards(body)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	if items[0].slug != "sexy-blonde-can-t-wait-for-his-hard-cock" {
		t.Errorf("slug[0] = %q", items[0].slug)
	}
	if items[0].title != "Sexy blonde can't wait for his hard cock" {
		t.Errorf("title[0] = %q", items[0].title)
	}
	if items[0].thumb != "https://img2.porncz.com/en/media/abc-video_list.webp" {
		t.Errorf("thumb[0] = %q", items[0].thumb)
	}
	if items[0].site != "teenfrombohemia.com" {
		t.Errorf("site[0] = %q", items[0].site)
	}

	if items[1].slug != "lusy-and-daniel-loves-it-hard" {
		t.Errorf("slug[1] = %q", items[1].slug)
	}
}

func TestParseListingCardsDedupe(t *testing.T) {
	body := []byte(`<div class="card card--item video-thumbnails" data-type="video">
<div class="card__img"><img alt="Scene 1" data-src="https://img2.porncz.com/thumb.webp"></div>
<div class="card-body"><a href="/en/same-slug" class="card__link">Scene 1</a></div>
</div></div></div>
<div class="card card--item video-thumbnails" data-type="video">
<div class="card__img"><img alt="Scene 1 dup" data-src="https://img2.porncz.com/thumb2.webp"></div>
<div class="card-body"><a href="/en/same-slug" class="card__link">Scene 1 dup</a></div>
</div></div></div>`)

	items := parseListingCards(body)
	if len(items) != 1 {
		t.Errorf("got %d items, want 1 (dedup)", len(items))
	}
}

func TestParseTotalPages(t *testing.T) {
	body := []byte(`<a href="/en/videos?sort=new&amp;page=2">2</a>
<a href="/en/videos?sort=new&amp;page=3">3</a>
<a href="/en/videos?sort=new&amp;page=79">79</a>`)
	if got := parseTotalPages(body); got != 79 {
		t.Errorf("parseTotalPages = %d, want 79", got)
	}
}

const listingTpl = `<div class="card card--item video-thumbnails" data-type="video"
	data-video="https://play.porncz.com/thumbnail/uuid-%d">
<div class="card__img">
	<img data-src="https://img2.porncz.com/thumb-%d.webp" alt="Scene %d" class="card-img-top img-fluid dmb-6">
</div>
<div class="card-body">
	<a href="/en/scene-%d" class="card__link stretched-link dmb-1">Scene %d</a>
</div>
</div></div></div>`

const detailTpl = `<html><head>
<script type="application/ld+json">
{
  "@type": "VideoObject",
  "name": "Scene %d",
  "description": "Description for scene %d.",
  "thumbnailUrl": "https://img2.porncz.com/detail-%d.webp",
  "uploadDate": "2026-01-15T00:00:00+01:00",
  "duration": "PT10M30S",
  "actor": [{"@type":"Person","name":"Performer %d"}],
  "partOfSeries": {"@type":"VideoSeries","name":"Test Project"}
}
</script>
</head></html>`

func buildListingPage(ids []int) string {
	var sb strings.Builder
	for _, id := range ids {
		_, _ = fmt.Fprintf(&sb, listingTpl, id, id, id, id, id)
	}
	return sb.String()
}

func newTestServer(ids []int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/en/videos":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, buildListingPage(ids))
		case strings.HasPrefix(r.URL.Path, "/en/scene-"):
			var id int
			_, _ = fmt.Sscanf(r.URL.Path, "/en/scene-%d", &id)
			if id > 0 {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprintf(w, detailTpl, id, id, id, id)
			} else {
				w.WriteHeader(404)
			}
		case r.URL.Path == "/en/pornstars/test-star":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, buildListingPage(ids[:min(2, len(ids))]))
		default:
			w.WriteHeader(404)
		}
	}))
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
		if sc.Duration != 630 {
			t.Errorf("duration = %d, want 630", sc.Duration)
		}
		if len(sc.Performers) != 1 {
			t.Errorf("performers = %v", sc.Performers)
		}
		if sc.Series != "Test Project" {
			t.Errorf("series = %q", sc.Series)
		}
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	ts := newTestServer([]int{3, 2, 1})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"scene-2": true},
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

// The scraper used to silently raise a zero delay to its own 500ms floor, so
// `--delay 0` cost half a second per request against an operator's own mirror.
// Delay control belongs to the config/CLI layer; the package only recommends.
func TestZeroDelayIsHonoured(t *testing.T) {
	ts := newTestServer([]int{3, 2, 1})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}

	start := time.Now()
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{Delay: 0, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(testutil.CollectScenes(t, ch)); n != 3 {
		t.Fatalf("got %d scenes, want 3", n)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("a zero delay took %v — the package is still applying a floor", elapsed)
	}
}
