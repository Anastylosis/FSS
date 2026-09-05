package thehabibshow

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://thehabibshow.com", true},
		{"https://www.thehabibshow.com/tour/", true},
		{"https://thehabibshow.com/tour/page7.html", true},
		{"https://thehabibshow.com/tour/channels/18/asian-porn/", true},
		{"https://thehabibshow.net/members/tube/", false},
		{"https://habibshow.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestListingPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://thehabibshow.com", "/tour/"},
		{"https://thehabibshow.com/", "/tour/"},
		{"https://thehabibshow.com/tour/", "/tour/"},
		{"https://thehabibshow.com/tour/page9.html", "/tour/"},
		{"https://thehabibshow.com/tour/channels/18/asian-porn/", "/tour/channels/18/asian-porn/"},
		{"https://thehabibshow.com/tour/channels/18/asian-porn", "/tour/channels/18/asian-porn/"},
		{"https://thehabibshow.com/tour/channels/18/asian-porn/page3.html", "/tour/channels/18/asian-porn/"},
		// Not a channel directory, so the walk falls back to the whole tour
		// rather than requesting pageN.html under a page that has none.
		{"https://thehabibshow.com/tour/faq.php", "/tour/"},
	}
	for _, c := range cases {
		if got := listingPath(c.in); got != c.want {
			t.Errorf("listingPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func article(id int, title, desc string) string {
	return fmt.Sprintf(`<article class="article">
	<header><h2 class="small font-color-orange">%s</h2></header>
	<div id="player-%d" class="player" data-id="%d"`+
		` data-poster="https://cdn.example.com/thumbs/%d.jpg"`+
		` data-video-hd="https://cdn.example.com/videos/%d.mp4"></div>
	<label for="video-url-%d"><input value="https://thehabibshow.com/tour/videos/scene-%d.html"></label>
	<p>%s</p>
	<a href="https://thehabibshow.com/tour/signup.php">Join Now!</a>
</article>`, title, id, id, id, id, id, id, desc)
}

func TestParseFeed(t *testing.T) {
	page := "<html><body><div class=\"article-feed\">" +
		article(2311, "BLACK ON BLACK PORN VIDEO... CHANNEL STAR&#39;S HOMECUMMING",
			"Yo, Channel Star slid back through.<br>The full Black on Black porn video is 36 minutes. SO, Join Now") +
		article(2309, "JOSH BONNET BREAKS THE FREAK SCALE", "No runtime named here.") +
		"</div></body></html>"

	items := parseFeed([]byte(page), "https://thehabibshow.com/tour/page1.html")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	first := items[0]
	if first.id != "2311" {
		t.Errorf("id = %q", first.id)
	}
	if first.title != "BLACK ON BLACK PORN VIDEO... CHANNEL STAR'S HOMECUMMING" {
		t.Errorf("title = %q", first.title)
	}
	if first.url != "https://thehabibshow.com/tour/videos/scene-2311.html" {
		t.Errorf("url = %q", first.url)
	}
	if first.thumbnail != "https://cdn.example.com/thumbs/2311.jpg" {
		t.Errorf("thumbnail = %q", first.thumbnail)
	}
	if first.duration != 36*60 {
		t.Errorf("duration = %d, want 2160", first.duration)
	}
	if !strings.HasPrefix(first.description, "Yo, Channel Star slid back through.") {
		t.Errorf("description = %q", first.description)
	}

	// The runtime is only ever stated inside the copy; an article that omits it
	// gets a zero duration rather than borrowing its neighbour's.
	if items[1].duration != 0 {
		t.Errorf("second duration = %d, want 0", items[1].duration)
	}
	if items[1].id != "2309" {
		t.Errorf("second id = %q", items[1].id)
	}
}

// Every field is read out of the article it belongs to; a page where one
// article omits a poster or a link must not shift the rest.
func TestParseFeedKeepsArticlesSeparate(t *testing.T) {
	bare := `<article class="article">
	<header><h2 class="small">Bare Scene</h2></header>
	<div id="player-1" class="player" data-id="100"></div>
	<p>No poster, no canonical link.</p>
</article>`
	page := bare + article(200, "Full Scene", "The full video is 12 minutes.")

	items := parseFeed([]byte(page), "https://thehabibshow.com/tour/page4.html")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].thumbnail != "" {
		t.Errorf("bare article borrowed a thumbnail: %q", items[0].thumbnail)
	}
	// With no canonical link the page it was found on is the honest fallback.
	if items[0].url != "https://thehabibshow.com/tour/page4.html" {
		t.Errorf("bare article url = %q", items[0].url)
	}
	if items[0].duration != 0 {
		t.Errorf("bare article duration = %d, want 0", items[0].duration)
	}
	if items[1].thumbnail != "https://cdn.example.com/thumbs/200.jpg" || items[1].duration != 720 {
		t.Errorf("second article lost its own fields: %+v", items[1])
	}
}

// ---- end-to-end ----

var pageNumRe = regexp.MustCompile(`page(\d+)\.html$`)

type fakeSite struct {
	pages   map[string]int // listing path -> number of full pages
	tail    int            // items on the last page
	visited []string
}

func (f *fakeSite) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := pageNumRe.FindStringSubmatch(r.URL.Path)
		if m == nil {
			http.NotFound(w, r)
			return
		}
		dir := r.URL.Path[:strings.LastIndex(r.URL.Path, "/")+1]
		total, ok := f.pages[dir]
		if !ok {
			t.Errorf("unexpected listing directory %q", dir)
			http.NotFound(w, r)
			return
		}
		f.visited = append(f.visited, r.URL.Path)
		page, _ := strconv.Atoi(m[1])
		if page > total {
			http.NotFound(w, r)
			return
		}
		n := pageSize
		if page == total {
			n = f.tail
		}
		var b strings.Builder
		b.WriteString("<html><body>")
		for i := range n {
			id := page*100 + i
			fmt.Fprint(&b, article(id, "Scene "+strconv.Itoa(id), "The full video is 20 minutes."))
		}
		b.WriteString("</body></html>")
		_, _ = fmt.Fprint(w, b.String())
	}
}

func newTestScraper(t *testing.T, f *fakeSite) *Scraper {
	t.Helper()
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	return &Scraper{Client: ts.Client(), base: ts.URL}
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]string, []error) {
	t.Helper()
	out := make(chan scraper.SceneResult, 500)
	go s.run(context.Background(), studioURL, opts, out)
	var ids []string
	var errs []error
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	return ids, errs
}

func TestRunWalksEveryPage(t *testing.T) {
	f := &fakeSite{pages: map[string]int{"/tour/": 3}, tail: 4}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://thehabibshow.com", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(ids) != 2*pageSize+4 {
		t.Errorf("got %d scenes, want %d", len(ids), 2*pageSize+4)
	}
	// A short last page ends the walk; asking for page 4 would be wasted.
	if slices.Contains(f.visited, "/tour/page4.html") {
		t.Error("requested a page past the short final one")
	}
}

// A feed whose last page happens to be full has no other end marker, so the
// walk finds the end by asking for one page too many. That 404 must stop the
// run quietly — reporting it would mark every full run incomplete and block
// the authoritative Save.
func TestRunTreatsA404PastTheEndAsDone(t *testing.T) {
	f := &fakeSite{pages: map[string]int{"/tour/": 2}, tail: pageSize}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://thehabibshow.com", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(ids) != 2*pageSize {
		t.Errorf("got %d scenes, want %d", len(ids), 2*pageSize)
	}
	if !slices.Contains(f.visited, "/tour/page3.html") {
		t.Error("expected the walk to probe one page past the end")
	}
}

// A 404 on page 1 is a broken scraper or a moved tour, not an empty catalogue.
func TestRunReportsAFirstPageFailure(t *testing.T) {
	f := &fakeSite{pages: map[string]int{"/tour/": 0}, tail: 0}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://thehabibshow.com", scraper.ListOpts{})
	if len(ids) != 0 {
		t.Errorf("got %d scenes, want none", len(ids))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
}

func TestRunChannel(t *testing.T) {
	f := &fakeSite{pages: map[string]int{"/tour/channels/18/asian-porn/": 1}, tail: 3}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, s.base+"/tour/channels/18/asian-porn/", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(ids) != 3 {
		t.Errorf("got %d scenes, want 3", len(ids))
	}
	if !slices.Contains(f.visited, "/tour/channels/18/asian-porn/page1.html") {
		t.Errorf("visited %v, want the channel's own pages", f.visited)
	}
}
