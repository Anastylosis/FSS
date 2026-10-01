package hustler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func listingItem(slug, title, thumb, channelSlug, channel string) string {
	return fmt.Sprintf(`
              <div class="__item">
                  <div class="__imgwrap no-trailer">
                      <img src="/hh-thumbnail/%s.jpg" title="%s Cover">
                      <a class="coverlink" href="https://hustlerunlimited.com/videos/%s/">%s</a>
                  <video preload="none" muted loop class="__rollover d-none" data-filename="%s">
 <source src="/hh-rollover/%s.mp4" type="video/mp4"></source>
                  </video>
                      <div class="__scenecount"><i class="fa-regular fa-circle-play"></i> 4 Scenes</div>
                  </div>
                  <h5 class="fs-heading-5">
                      <a href="https://hustlerunlimited.com/videos/%s/">%s</a></h5>
                  <h6 class="">
                      <a href="/videos/?_sft_video_channels=%s">
                      <i class="fa-solid fa-tv"></i> %s</a></h6>
              </div>`, thumb, title, slug, title, thumb, thumb, slug, title, channelSlug, channel)
}

func listingPage(pager string, items ...string) string {
	body := `<html><body><div class="videos-wrap">`
	for _, it := range items {
		body += it
	}
	return body + `</div><nav aria-label="Page navigation"><ul class="pagination">` + pager + `</ul></nav></body></html>`
}

const pager2 = `<li class="page-item"><a class="page-link" href="https://hustlerunlimited.com/videos/?sf_paged=2">2</a></li>`

func detailPage(postID, published string, performers ...string) string {
	body := fmt.Sprintf(`<html><head><script type="application/ld+json">{"@type":"WebPage","datePublished":"%s"}</script></head>
<body class="videos-template-default single single-videos postid-%s">
<div class="pornstars-wrapper justify-content-center">`, published, postID)
	for _, p := range performers {
		body += fmt.Sprintf(`
  <div class="__item">
    <a class="coverlink" href="https://hustlerunlimited.com/model/x/">Movie</a>
    <div class="__img" style="background-image: url('/hh-stars/x-profile.jpg');"></div>
    <h5 class="fs-heading-6">%s</h5>
  </div>`, p)
	}
	return body + `</div></div></div>
<h4 class="fs-heading-3 mb-4">More Like This</h4>
<div class="swiper-slide"><h5 class="fs-heading-6">Not A Performer</h5></div>
</body></html>`
}

func TestParseListing(t *testing.T) {
	body := listingPage(pager2,
		listingItem("my-neighbors-nanny", "My Neighbor&#8217;s Nanny", "00844688", "hustler", "HUSTLER"),
		listingItem("barely-legal-149", "Barely Legal 149", "00477369", "barelylegal", "Barely Legal"),
	)
	items := parseListing([]byte(body))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	it := items[0]
	if it.url != "https://hustlerunlimited.com/videos/my-neighbors-nanny/" {
		t.Errorf("url = %q", it.url)
	}
	if it.title != "My Neighbor’s Nanny" {
		t.Errorf("title = %q (want HTML-unescaped)", it.title)
	}
	if it.thumbnail != "https://hustlerunlimited.com/hh-thumbnail/00844688.jpg" {
		t.Errorf("thumbnail = %q", it.thumbnail)
	}
	if it.channel != "Hustler" {
		t.Errorf("channel = %q, want de-shouted Hustler", it.channel)
	}
	if items[1].channel != "Barely Legal" {
		t.Errorf("channel = %q", items[1].channel)
	}
	if got := parseLastPage([]byte(body)); got != 2 {
		t.Errorf("parseLastPage = %d, want 2", got)
	}
}

func TestParseDetail(t *testing.T) {
	d, err := parseDetail([]byte(detailPage("68667", "2026-09-26T08:49:28+00:00", "Mia River", "Mira &amp; Luv")))
	if err != nil {
		t.Fatalf("parseDetail: %v", err)
	}
	if d.id != "68667" {
		t.Errorf("id = %q", d.id)
	}
	if want := time.Date(2026, 9, 26, 8, 49, 28, 0, time.UTC); !d.date.Equal(want) {
		t.Errorf("date = %v, want %v", d.date, want)
	}
	if len(d.performers) != 2 || d.performers[0] != "Mia River" || d.performers[1] != "Mira & Luv" {
		t.Errorf("performers = %v (the More Like This heading must be excluded)", d.performers)
	}
}

func TestParseDetail_noPostIDIsParseError(t *testing.T) {
	if _, err := parseDetail([]byte(`<html><body>gone</body></html>`)); err == nil {
		t.Fatal("expected an error for a page without a post ID")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://hustlerunlimited.com/", true},
		{"http://hustlerunlimited.com", true},
		{"https://hustlerunlimited.com/videos/", true},
		{"https://www.hustlerunlimited.com/videos/?_sft_video_channels=barelylegal", true},
		{"https://hustlerunlimited.com/model/dee-williams/", true},
		{"https://hustlerunlimited.com/videos/my-neighbors-nanny/", false},
		{"https://example.com/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

func TestPatternsCoverDispatchedForms(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://hustlerunlimited.com/videos/",
		"https://hustlerunlimited.com/videos/?_sft_video_tags=anal",
		"https://hustlerunlimited.com/model/dee-williams/",
	} {
		if scraper.URLLooksUnhandled(s, u) {
			t.Errorf("URLLooksUnhandled(%q) = true, want false", u)
		}
	}
}

func TestListingFilter(t *testing.T) {
	cases := map[string]string{
		"https://hustlerunlimited.com/":                                              "",
		"https://hustlerunlimited.com/videos/?_sft_video_channels=barelylegal":       "_sft_video_channels=barelylegal",
		"https://hustlerunlimited.com/videos/?_sft_video_tags=anal&utm_source=x":     "_sft_video_tags=anal",
		"https://hustlerunlimited.com/model/dee-williams/":                           "_sft_hu_actors=dee-williams",
		"https://hustlerunlimited.com/videos/?_sft_hu_actors=billy-glide&sf_paged=3": "_sft_hu_actors=billy-glide",
	}
	for in, want := range cases {
		if got := listingFilter(in).Encode(); got != want {
			t.Errorf("listingFilter(%q) = %q, want %q", in, got, want)
		}
	}
}

func newTestServer(t *testing.T, pages map[string]string, details map[string]string) (*httptest.Server, *sync.Map) {
	t.Helper()
	var seen sync.Map
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.URL.String(), true)
		if r.URL.Path == "/videos/" {
			q := r.URL.Query()
			page := q.Get("sf_paged")
			if page == "" {
				page = "1"
			}
			q.Del("sf_paged")
			key := q.Encode() + "#" + page
			if body, ok := pages[key]; ok {
				_, _ = fmt.Fprint(w, body)
				return
			}
			_, _ = fmt.Fprint(w, listingPage(""))
			return
		}
		if body, ok := details[r.URL.Path]; ok {
			_, _ = fmt.Fprint(w, body)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	orig := siteBase
	siteBase = ts.URL
	t.Cleanup(func() { siteBase = orig })
	return ts, &seen
}

func collect(t *testing.T, ch <-chan scraper.SceneResult) (ids []string, titles map[string]string, total int, errs []error, stopped bool) {
	t.Helper()
	titles = map[string]string{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
			titles[r.Scene.ID] = r.Scene.Title
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindError:
			errs = append(errs, r.Err)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return
}

func TestListScenes_endToEnd(t *testing.T) {
	pages := map[string]string{
		"#1": listingPage(pager2,
			listingItem("a", "Title A", "001", "hustler", "HUSTLER"),
			listingItem("b", "Title B", "002", "barelylegal", "Barely Legal"),
		),
		"#2": listingPage(pager2,
			listingItem("c", "Title C", "003", "hustler", "HUSTLER"),
		),
	}
	details := map[string]string{
		"/videos/a/": detailPage("300", "2026-09-30T08:00:00+00:00", "Ann"),
		"/videos/b/": detailPage("200", "2026-09-20T08:00:00+00:00", "Bea", "Cat"),
		"/videos/c/": detailPage("100", "2026-09-10T08:00:00+00:00"),
	}
	_, _ = newTestServer(t, pages, details)

	ch, err := New().ListScenes(context.Background(), "https://hustlerunlimited.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	ids, titles, total, errs, _ := collect(t, ch)
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if fmt.Sprint(ids) != "[300 200 100]" {
		t.Fatalf("ids = %v, want listing order [300 200 100]", ids)
	}
	if titles["200"] != "Title B" {
		t.Errorf("title = %q", titles["200"])
	}
	if total != 4 {
		t.Errorf("total = %d, want 4 (2 pages x 2 items)", total)
	}
}

func TestListScenes_sceneFields(t *testing.T) {
	pages := map[string]string{
		"#1": listingPage("", listingItem("b", "Title &amp; B", "002", "barelylegal", "Barely Legal")),
	}
	details := map[string]string{
		"/videos/b/": detailPage("200", "2026-09-20T08:00:00+00:00", "Bea", "Cat"),
	}
	_, _ = newTestServer(t, pages, details)

	ch, _ := New().ListScenes(context.Background(), "https://hustlerunlimited.com/", scraper.ListOpts{})
	var got []scraper.SceneResult
	for r := range ch {
		if r.Kind == scraper.KindScene {
			got = append(got, r)
		}
	}
	if len(got) != 1 {
		t.Fatalf("got %d scenes, want 1", len(got))
	}
	sc := got[0].Scene
	if sc.ID != "200" || sc.SiteID != "hustler" || sc.StudioURL != "https://hustlerunlimited.com/" {
		t.Errorf("identity = %q/%q/%q", sc.ID, sc.SiteID, sc.StudioURL)
	}
	if sc.Title != "Title & B" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.URL != "https://hustlerunlimited.com/videos/b/" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Thumbnail != "https://hustlerunlimited.com/hh-thumbnail/002.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.Series != "Barely Legal" || len(sc.Categories) != 1 {
		t.Errorf("Series/Categories = %q/%v", sc.Series, sc.Categories)
	}
	if sc.Studio != "Hustler" {
		t.Errorf("Studio = %q", sc.Studio)
	}
	if len(sc.Performers) != 2 || sc.Performers[1] != "Cat" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if want := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("Date = %v", sc.Date)
	}
}

func TestListScenes_modelPageUsesActorFilter(t *testing.T) {
	pages := map[string]string{
		"_sft_hu_actors=dee-williams#1": listingPage("", listingItem("a", "Title A", "001", "hustler", "HUSTLER")),
	}
	details := map[string]string{
		"/videos/a/": detailPage("300", "2026-09-30T08:00:00+00:00", "Dee Williams"),
	}
	_, seen := newTestServer(t, pages, details)

	ch, _ := New().ListScenes(context.Background(), "https://hustlerunlimited.com/model/dee-williams/", scraper.ListOpts{})
	ids, _, _, errs, _ := collect(t, ch)
	if len(errs) > 0 || fmt.Sprint(ids) != "[300]" {
		t.Fatalf("ids = %v errs = %v", ids, errs)
	}
	if _, ok := seen.Load("/videos/?" + url.Values{"_sft_hu_actors": {"dee-williams"}}.Encode()); !ok {
		t.Error("model page was not mapped onto the _sft_hu_actors listing filter")
	}
}

func TestListScenes_knownIDsStopAfterPage(t *testing.T) {
	pages := map[string]string{
		"#1": listingPage(pager2,
			listingItem("a", "Title A", "001", "hustler", "HUSTLER"),
			listingItem("b", "Title B", "002", "hustler", "HUSTLER"),
		),
		"#2": listingPage(pager2, listingItem("c", "Title C", "003", "hustler", "HUSTLER")),
	}
	details := map[string]string{
		"/videos/a/": detailPage("300", "2026-09-30T08:00:00+00:00"),
		"/videos/b/": detailPage("200", "2026-09-20T08:00:00+00:00"),
		"/videos/c/": detailPage("100", "2026-09-10T08:00:00+00:00"),
	}
	_, seen := newTestServer(t, pages, details)

	ch, _ := New().ListScenes(context.Background(), "https://hustlerunlimited.com/", scraper.ListOpts{KnownIDs: map[string]bool{"200": true}})
	ids, _, _, _, stopped := collect(t, ch)
	if fmt.Sprint(ids) != "[300]" || !stopped {
		t.Fatalf("ids = %v stopped = %v, want [300] and an early stop", ids, stopped)
	}
	if _, ok := seen.Load("/videos/?sf_paged=2"); ok {
		t.Error("page 2 fetched after a known ID")
	}
}

func TestListScenes_failedDetailIsReported(t *testing.T) {
	pages := map[string]string{
		"#1": listingPage("",
			listingItem("a", "Title A", "001", "hustler", "HUSTLER"),
			listingItem("gone", "Gone", "002", "hustler", "HUSTLER"),
		),
	}
	details := map[string]string{
		"/videos/a/": detailPage("300", "2026-09-30T08:00:00+00:00"),
	}
	_, _ = newTestServer(t, pages, details)

	ch, _ := New().ListScenes(context.Background(), "https://hustlerunlimited.com/", scraper.ListOpts{})
	ids, _, _, errs, _ := collect(t, ch)
	if fmt.Sprint(ids) != "[300]" {
		t.Errorf("ids = %v, want [300]", ids)
	}
	if len(errs) != 1 {
		t.Errorf("errs = %v, want one for the missing title page", errs)
	}
}
