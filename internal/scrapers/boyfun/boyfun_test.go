package boyfun

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

// ---- fixtures ----

func listingHTML(base string) string {
	card := func(slug, id, title, date string) string {
		return fmt.Sprintf(`<div class="item tour-content-card">
  <div class="item-inside">
    <a href="%s/video/%s-%s.html">
      <div class="thumb">
        <img src="https://media.boyfun.com/thumbs_pg/%s.jpg" loading="lazy" alt="%s" data-card-fallbacks="[&quot;https:\/\/www.boyfun.com\/placeholder.svg&quot;]">
        <div class="overlay"><div class="meta">
          <span class="title">%s</span>
          <span class="date">%s</span>
        </div></div>
      </div>
    </a>
  </div>
</div>`, base, slug, id, id, title, title, date)
	}
	return `<html><body><nav><div class="item item-videos"><a href="` + base + `/videos/">Videos</a></div></nav>` +
		card("dirty-daydream", "15957", "Dirty Daydream: Part 1", "26 Jun 2026") +
		card("beach-fun", "15940", "Beach Fun &amp; Sun", "20 Jun 2026") +
		// duplicate id should be deduped
		card("dirty-daydream", "15957", "Dirty Daydream: Part 1", "26 Jun 2026") +
		"</body></html>"
}

func detailHTML() string {
	return `<html><body>
<div class="video-poster">
  <img src="https://media.boyfun.com/thumbs_pg/015957-feat_lg.jpg" class="poster" style="width: auto;" alt="Video preview" loading="eager" decoding="async">
</div>
<div class="content-information-meta cf">
  <span class="models">
    <span class="heading">Starring:</span>
    <span class="content"><a href='/models/jack-angeli-3515.html'>Jack Angeli</a>, <a href='/models/xean-piere-3523.html'>Xean Piere</a></span>
  </span>
  <span class="date">
    <span class="heading">Added:</span>
    <span class="content">26 Jun 2026</span>
  </span>
</div>
<div class="content-information-description">
  <div class="heading">Description: </div>
  <div class="tour-detail-description-body">Xean and Jack get very messy in the kitchen.

They clean up afterwards &amp; start again.</div>
</div>
</body></html>`
}

// ---- TestMatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.boyfun.com/videos/", true},
		{"https://boyfun.com/video/foo-123.html", true},
		{"https://example.com/x", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// ---- TestFetchListing ----

func TestFetchListing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, listingHTML(""))
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client()}
	items, err := s.fetchListing(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("fetchListing error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (dup dropped): %+v", len(items), items)
	}
	if items[0].id != "15957" || items[0].title != "Dirty Daydream: Part 1" {
		t.Errorf("item0 = %+v", items[0])
	}
	if items[1].title != "Beach Fun & Sun" {
		t.Errorf("item1 title = %q", items[1].title)
	}
	if items[0].date != "26 Jun 2026" {
		t.Errorf("item0 date = %q", items[0].date)
	}
	if items[0].thumbnail != "https://media.boyfun.com/thumbs_pg/15957.jpg" {
		t.Errorf("item0 thumbnail = %q", items[0].thumbnail)
	}
}

func TestParseLegacyDetail(t *testing.T) {
	legacy := `<span class="date"><span class="heading">Added:</span><span class="content">Jun 26th, 2026</span></span>
<div class="content-information-description"><div class="heading">Description: </div><p>Old layout.<br></p></div>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, legacy)
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client()}
	sc := s.toScene(context.Background(), "studioURL", listItem{id: "1", url: ts.URL}, time.Now())
	if want := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
	if sc.Description != "Old layout." {
		t.Errorf("Description = %q", sc.Description)
	}
}

func TestListScenesEmptyListingIsParseError(t *testing.T) {
	orig := siteBase
	defer func() { siteBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body><div class="warning-overlay">Adults only</div></body></html>`)
	}))
	defer ts.Close()
	siteBase = ts.URL

	s := &Scraper{Client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), "studioURL", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	var errs []error
	for r := range ch {
		if r.Kind == scraper.KindScene {
			t.Errorf("unexpected scene %+v", r.Scene)
		}
		if r.Err != nil {
			errs = append(errs, r.Err)
		}
	}
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Fatalf("errors = %v, want one parse error", errs)
	}
}

// ---- TestToScene (detail parse) ----

func TestToScene(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, detailHTML())
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client()}
	it := listItem{id: "15957", url: ts.URL + "/video/dirty-daydream-15957.html", title: "Dirty Daydream: Part 1", date: "26 Jun 2026"}
	now := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)
	sc := s.toScene(context.Background(), "studioURL", it, now)

	if sc.ID != "15957" || sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("identity = %+v", sc)
	}
	if sc.Title != "Dirty Daydream: Part 1" {
		t.Errorf("Title = %q", sc.Title)
	}
	wantDate := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)
	if !sc.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", sc.Date, wantDate)
	}
	if strings.Join(sc.Performers, ",") != "Jack Angeli,Xean Piere" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Description != "Xean and Jack get very messy in the kitchen. They clean up afterwards & start again." {
		t.Errorf("Description = %q", sc.Description)
	}
	if sc.Thumbnail != "https://media.boyfun.com/thumbs_pg/015957-feat_lg.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
}

// ---- TestListScenes (end-to-end) ----

func TestListScenes(t *testing.T) {
	orig := siteBase
	defer func() { siteBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/videos/":
			_, _ = fmt.Fprint(w, listingHTML(siteBase))
		case strings.HasPrefix(r.URL.Path, "/video/"):
			_, _ = fmt.Fprint(w, detailHTML())
		default:
			// page2.html etc. repeat the same cards -> dedup -> Done.
			_, _ = fmt.Fprint(w, listingHTML(siteBase))
		}
	}))
	defer ts.Close()
	siteBase = ts.URL

	s := &Scraper{Client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), "studioURL", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	got := map[string]string{}
	for r := range ch {
		if r.Err != nil {
			t.Errorf("unexpected error: %v", r.Err)
			continue
		}
		if r.Kind == scraper.KindScene {
			got[r.Scene.ID] = r.Scene.Title
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(got), got)
	}
	if got["15957"] != "Dirty Daydream: Part 1" {
		t.Errorf("scenes = %v", got)
	}
}
