package cruelgirlfriend

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://cruelgf.com/", true},
		{"https://www.cruelgf.com/CGUpdates.php", true},
		{"http://cruelgf.com/Girl.php?girlfriend=Zoe%20Grey", true},
		{"https://cruelgf.com/Clip.php?clip_no=1059", true},
		{"https://notcruelgf.com/", false},
		{"https://cruelgf.example.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestGirlfriendOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://cruelgf.com/Girl.php?girlfriend=Zoe Grey", "Zoe Grey"},
		{"https://cruelgf.com/Girl.php?girlfriend=Zoe%20Grey", "Zoe Grey"},
		{"https://cruelgf.com/", ""},
		{"https://cruelgf.com/CGUpdates.php", ""},
	}
	for _, c := range cases {
		if got := girlfriendOf(c.in); got != c.want {
			t.Errorf("girlfriendOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		body string
		want int
	}{
		{`<span>Duration: 15m 59s</span>`, 959},
		{`<span>Duration: 05m 36s</span>`, 336},
		{`<span>Duration: 1h 02m 30s</span>`, 3750},
		{`<span>Duration: 16m 00s</span>`, 960},
		{`<span>Formats: HD</span>`, 0},
	}
	for _, c := range cases {
		if got := parseDuration([]byte(c.body)); got != c.want {
			t.Errorf("parseDuration(%q) = %d, want %d", c.body, got, c.want)
		}
	}
}

func TestSplitCast(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Jessie - Cashleigh", []string{"Jessie", "Cashleigh"}},
		{"Honour", []string{"Honour"}},
		// A hyphen with no spaces belongs to the name, not to the separator.
		{"Anne-Marie", []string{"Anne-Marie"}},
		{"Zoe - Zoe", []string{"Zoe"}},
		{"", nil},
	}
	for _, c := range cases {
		if got := splitCast(c.in); !slices.Equal(got, c.want) {
			t.Errorf("splitCast(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// The site emits its description into the JSON-LD with raw newlines, so the
// schema.org block is invalid JSON on exactly the clips with the longest copy.
// Everything must come off the HTML instead.
const brokenLDClip = `<html><head>
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@type": "VideoObject",
  "name": "Choose Humiliporn",
  "description": "line one

 ...",
  "uploadDate": "2026-08-24T08:00:00+08:00",
  "duration": "PT16M16S"
}
</script></head><body>
<div class="media-video-copy">
	<h3>Choose Humiliporn</h3>
	<p>Jessie - Cashleigh</p>
</div>
<section class="media-panel media-panel--details">
	<h3>Choose Humiliporn</h3>
	<div class="clip-description">
		Prepare to burn your entire world down.&lt;br&gt;&lt;br&gt;It&#039;s all going.
	</div>
	<div class="media-meta">
		<span>Added: 24-08-2026</span>
		<span>Duration: 16m 04s</span>
	</div>
	<a href="Category.php?category=Humiliation">Humiliation</a>
	<a href="Category.php?category=Public%20Humiliation">Public Humiliation</a>
	<a href="Category.php?category=Humiliation">Humiliation</a>
</section>
<img id="videoPosterOverlay" class="videoless-poster-overlay" src="images/Backgrounds/2190-01.jpg" alt="x">
</body></html>`

func TestParseClip(t *testing.T) {
	sc, err := parseClip([]byte(brokenLDClip), 2190,
		"https://cruelgf.com/Clip.php?clip_no=2190", "https://cruelgf.com")
	if err != nil {
		t.Fatal(err)
	}
	if sc.ID != "2190" || sc.SiteID != siteID {
		t.Errorf("id/site = %q/%q", sc.ID, sc.SiteID)
	}
	if sc.Title != "Choose Humiliporn" {
		t.Errorf("title = %q", sc.Title)
	}
	if !slices.Equal(sc.Performers, []string{"Jessie", "Cashleigh"}) {
		t.Errorf("performers = %v", sc.Performers)
	}
	// Double-encoded markup must not survive into the description.
	want := "Prepare to burn your entire world down. It's all going."
	if sc.Description != want {
		t.Errorf("description = %q, want %q", sc.Description, want)
	}
	if sc.Date.Format("2006-01-02") != "2026-08-24" {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Duration != 964 {
		t.Errorf("duration = %d, want 964", sc.Duration)
	}
	if sc.Thumbnail != "https://cruelgf.com/images/Backgrounds/2190-01.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if !slices.Equal(sc.Categories, []string{"Humiliation", "Public Humiliation"}) {
		t.Errorf("categories = %v", sc.Categories)
	}
}

func TestParseClipWithoutCopyBlockIsAParseError(t *testing.T) {
	_, err := parseClip([]byte(`<html><body><p>nothing here</p></body></html>`), 1,
		"https://cruelgf.com/Clip.php?clip_no=1", "https://cruelgf.com")
	if err == nil {
		t.Fatal("want an error")
	}
	if k := scraper.Classify(err); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

// ---- end-to-end ----

type fakeSite struct {
	clips    []int
	fetched  atomic.Int64
	sitemap  string
	girlPage string
}

func (f *fakeSite) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprint(w, f.sitemap)
		case "/Girl.php":
			_, _ = fmt.Fprint(w, f.girlPage)
		case "/Clip.php":
			f.fetched.Add(1)
			id := r.URL.Query().Get("clip_no")
			_, _ = fmt.Fprint(w, strings.ReplaceAll(strings.ReplaceAll(brokenLDClip,
				"Choose Humiliporn", "Clip "+id), "2190-01.jpg", id+".jpg"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}
}

func sitemapFor(base string, ids []int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	fmt.Fprintf(&b, `<url><loc>%s/index.php</loc></url>`, base)
	for _, id := range ids {
		fmt.Fprintf(&b, `<url><loc>%s/Clip.php?clip_no=%d</loc></url>`, base, id)
	}
	// A duplicate must not become a second scene.
	if len(ids) > 0 {
		fmt.Fprintf(&b, `<url><loc>%s/Clip.php?clip_no=%d</loc></url>`, base, ids[0])
	}
	b.WriteString(`</urlset>`)
	return b.String()
}

func newTestScraper(t *testing.T, f *fakeSite) *Scraper {
	t.Helper()
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	f.sitemap = sitemapFor(ts.URL, f.clips)
	return &Scraper{Client: ts.Client(), base: ts.URL}
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]string, bool, []error) {
	t.Helper()
	out := make(chan scraper.SceneResult, 500)
	go s.run(context.Background(), studioURL, opts, out)
	var ids []string
	var errs []error
	stopped := false
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			errs = append(errs, r.Err)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return ids, stopped, errs
}

// The sitemap is unordered; clip numbers rise with upload date, so the walk
// must sort descending or the early stop fires against the wrong clip.
func TestRunWalksNewestFirstAndDeduplicates(t *testing.T) {
	f := &fakeSite{clips: []int{141, 2190, 995, 1795}}
	s := newTestScraper(t, f)

	ids, _, errs := collect(t, s, "https://cruelgf.com", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !slices.Equal(ids, []string{"2190", "1795", "995", "141"}) {
		t.Errorf("ids = %v, want newest first with no repeat", ids)
	}
}

func TestRunStopsAtAKnownID(t *testing.T) {
	var clips []int
	for i := range 3 * chunkSize {
		clips = append(clips, 1000+i)
	}
	f := &fakeSite{clips: clips}
	s := newTestScraper(t, f)

	// The known id sits in the first chunk, so the second chunk of details
	// must never be requested.
	known := strconv.Itoa(1000 + len(clips) - 3)
	ids, stopped, errs := collect(t, s, "https://cruelgf.com",
		scraper.ListOpts{KnownIDs: map[string]bool{known: true}})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if len(ids) > chunkSize {
		t.Errorf("emitted %d scenes, want at most one chunk", len(ids))
	}
	if got := f.fetched.Load(); got > int64(chunkSize) {
		t.Errorf("fetched %d clips, want at most one chunk of %d", got, chunkSize)
	}
}

func TestRunGirlfriendPage(t *testing.T) {
	f := &fakeSite{girlPage: `<html><body>
		<a href="Clip.php?clip_no=1587">a</a>
		<a href="Clip.php?clip_no=1400">b</a>
		<a href="Clip.php?clip_no=1587">a again</a>
	</body></html>`}
	s := newTestScraper(t, f)

	ids, _, errs := collect(t, s, s.base+"/Girl.php?girlfriend=Zoe%20Grey", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !slices.Equal(ids, []string{"1587", "1400"}) {
		t.Errorf("ids = %v", ids)
	}
}

func TestRunGirlfriendPageWithNoClipsReportsAParseError(t *testing.T) {
	f := &fakeSite{girlPage: `<html><body><p>no such girlfriend</p></body></html>`}
	s := newTestScraper(t, f)

	_, _, errs := collect(t, s, s.base+"/Girl.php?girlfriend=Nobody", scraper.ListOpts{})
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}
