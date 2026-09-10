package princessrene

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

// listingFixture mirrors the real /videos/ card markup: real scene links plus
// category and pagination links that must be filtered out, with one slug
// repeated to exercise dedup.
const listingFixture = `
<div class="item item-video">
  <div class="item-image">
    <a href="https://worshiprene.com/videos/beg-to-see-my-tits"><img src="https://worshiprene.com/media/video/13359/x-640x360.jpg" /></a>
  </div>
  <div class="item-content">
    <div class="duration">09:40</div>
    <h3><a href="https://worshiprene.com/videos/beg-to-see-my-tits">Beg to See My Tits</a></h3>
    <div class="terms">
      <a href="https://worshiprene.com/videos/category/joi" class="term-link">JOI</a>
    </div>
  </div>
</div>
<div class="item item-video">
  <div class="item-content">
    <h3><a href="https://worshiprene.com/videos/jerk-for-me/">Jerk for Me</a></h3>
  </div>
</div>
<nav class="pagination">
  <a href="https://worshiprene.com/videos/page/2/">2</a>
</nav>
`

func TestParseListing(t *testing.T) {
	slugs := parseListing([]byte(listingFixture))
	want := []string{"beg-to-see-my-tits", "jerk-for-me"}
	if len(slugs) != len(want) {
		t.Fatalf("got %d slugs %v, want %d %v", len(slugs), slugs, len(want), want)
	}
	for i, w := range want {
		if slugs[i] != w {
			t.Errorf("slug[%d] = %q, want %q", i, slugs[i], w)
		}
	}
}

func TestParseListingEmpty(t *testing.T) {
	// A page with only category/pagination links yields no scenes (end of list).
	const noScenes = `
<a href="https://worshiprene.com/videos/category/feet">Feet</a>
<a href="https://worshiprene.com/videos/page/3/">3</a>
`
	if got := parseListing([]byte(noScenes)); len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

// detailFixture captures the real OpenGraph + Yoast JSON-LD shape of a detail
// page (with HTML-entity-encoded description and "- Princess Rene" title suffix).
const detailFixture = `<!DOCTYPE html><html><head>
<meta property="og:locale" content="en_US" />
<meta property="og:type" content="article" />
<meta property="og:title" content="Beg to See My Tits - Princess Rene" />
<meta property="og:description" content="There&#8217;s one part of me that you&#8217;ve always dreamed about seeing: my perky breasts &hellip;" />
<meta property="og:url" content="https://worshiprene.com/videos/beg-to-see-my-tits" />
<meta property="og:site_name" content="Princess Rene" />
<meta property="og:image" content="https://worshiprene.com/media/video/13359/princess-rene-beg-to-see-my-tits_frame.0000005.jpg" />
<script type="application/ld+json" class="yoast-schema-graph">{"@context":"https://schema.org","@graph":[{"@type":"WebPage","datePublished":"2026-05-25T20:58:28+00:00","dateModified":"2026-05-25T20:58:28+00:00"}]}</script>
</head><body></body></html>`

func TestParseDetail(t *testing.T) {
	d := parseDetail([]byte(detailFixture))

	if d.title != "Beg to See My Tits" {
		t.Errorf("title = %q, want %q", d.title, "Beg to See My Tits")
	}
	wantDesc := "There’s one part of me that you’ve always dreamed about seeing: my perky breasts …"
	if d.description != wantDesc {
		t.Errorf("description = %q, want %q", d.description, wantDesc)
	}
	if d.thumbnail != "https://worshiprene.com/media/video/13359/princess-rene-beg-to-see-my-tits_frame.0000005.jpg" {
		t.Errorf("thumbnail = %q", d.thumbnail)
	}
	want := time.Date(2026, 5, 25, 20, 58, 28, 0, time.UTC)
	if !d.date.Equal(want) {
		t.Errorf("date = %v, want %v", d.date, want)
	}
}

func TestSlugToTitle(t *testing.T) {
	if got := slugToTitle("beg-to-see-my-tits"); got != "beg to see my tits" {
		t.Errorf("got %q", got)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := map[string]bool{
		"https://worshiprene.com/videos/":            true,
		"https://www.worshiprene.com/videos/page/2/": true,
		"http://worshiprene.com":                     true,
		"https://meanawolf.com/videos/":              false,
		"https://worshipreneXcom/videos/":            false,
	}
	for u, want := range cases {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}

// --- end to end ---------------------------------------------------------------
//
// The tests above call the parsers directly, which left ListScenes, run,
// fetchPage and the paginated walk at 0% and the package at 37.0%. The base is
// now a field, so the listing -> detail walk runs offline.
func TestListScenesEndToEnd(t *testing.T) {
	// Handlers run on separate goroutines (Workers > 1), so these counters
	// must be atomic — a plain int++ here is a real data race.
	var listingHits atomic.Int32
	var detailHits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/videos/" || strings.HasPrefix(r.URL.Path, "/videos/page/"):
			listingHits.Add(1)
			// Only the first listing has cards; the next page is empty so the walk ends.
			if strings.Contains(r.URL.Path, "/page/2") {
				_, _ = fmt.Fprint(w, `<div class="empty"></div>`)
				return
			}
			_, _ = fmt.Fprint(w, listingFixture)
		case strings.HasPrefix(r.URL.Path, "/videos/"):
			detailHits.Add(1)
			// Give each slug its own og:url and title, so two scenes cannot share
			// them — the shared-fixture trap.
			slug := strings.Trim(strings.TrimPrefix(r.URL.Path, "/videos/"), "/")
			_, _ = fmt.Fprint(w, strings.ReplaceAll(detailFixture, "beg-to-see-my-tits", slug))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New()
	s.client = ts.Client()
	s.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	if listingHits.Load() == 0 || detailHits.Load() != 2 {
		t.Errorf("listing/detail fetches = %d/%d, want listing>0 and detail=2", listingHits.Load(), detailHits.Load())
	}

	seenURL := map[string]bool{}
	for _, sc := range scenes {
		if sc.Title == "" {
			t.Errorf("scene %q has empty Title", sc.ID)
		}
		if sc.Date.IsZero() {
			t.Errorf("scene %q has zero Date — the JSON-LD datePublished was not parsed", sc.ID)
		}
		if seenURL[sc.URL] {
			t.Errorf("scene %q repeats URL %q", sc.ID, sc.URL)
		}
		seenURL[sc.URL] = true
	}
}
