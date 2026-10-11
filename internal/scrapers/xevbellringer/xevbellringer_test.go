package xevbellringer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.xevunleashed.com/categories/movies.html", true},
		{"https://xevunleashed.com/categories/movies.html", true},
		{"https://www.xevunleashed.com/", true},
		{"https://www.xevunleashed.com/updates/Homecumming.html", true},
		{"https://example.com/xevunleashed", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestSlugFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.xevunleashed.com/updates/Homecumming.html", "Homecumming"},
		{"https://www.xevunleashed.com/updates/Holey-Knight.html", "Holey-Knight"},
		{"/updates/Some-Scene.html", "Some-Scene"},
	}
	for _, c := range cases {
		if got := slugFromURL(c.url); got != c.want {
			t.Errorf("slugFromURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestParseListingPage(t *testing.T) {
	body := []byte(`
<div class="updateItem">
	<a href="https://test.local/updates/Scene-One.html">
		<img src="content/Scene_One/1.jpg" />
	</a>
	<div class="updateDetails">
		<div class="cart_buttons cart_setid_18">
			<a href="access/scenes/Scene-One_vids.html?buy=1">
				<div class="buy_button">Buy $9.99</div>
			</a>
		</div>
		<h4>
			<a href="https://test.local/updates/Scene-One.html">
				Scene One
			</a>
		</h4>
		<p><span>02/10/2026</span></p>
	</div>
</div>
<div class="updateItem">
	<a href="https://test.local/updates/Scene-Two.html">
		<img src="content/Scene_Two/1.jpg" />
	</a>
	<div class="updateDetails">
		<div class="cart_buttons cart_setid_18">
			<a href="access/scenes/Scene-Two_vids.html?buy=1">
				<div class="buy_button">Buy $19.99</div>
			</a>
		</div>
		<h4>
			<a href="https://test.local/updates/Scene-Two.html">
				Scene Two
			</a>
		</h4>
		<p><span>01/15/2026</span></p>
	</div>
</div>
`)

	scenes := parseListingPage(body, "https://test.local")

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s := scenes[0]
	if s.id != "Scene-One" {
		t.Errorf("id = %q", s.id)
	}
	if s.title != "Scene One" {
		t.Errorf("title = %q", s.title)
	}
	if s.url != "https://test.local/updates/Scene-One.html" {
		t.Errorf("url = %q", s.url)
	}
	wantDate := time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)
	if !s.date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", s.date, wantDate)
	}
	if s.price != 9.99 {
		t.Errorf("price = %f, want 9.99", s.price)
	}
	if s.thumb != "https://test.local/content/Scene_One/1.jpg" {
		t.Errorf("thumb = %q", s.thumb)
	}

	s2 := scenes[1]
	if s2.id != "Scene-Two" {
		t.Errorf("id = %q", s2.id)
	}
	if s2.price != 19.99 {
		t.Errorf("price = %f, want 19.99", s2.price)
	}
}

func TestParseDetailPage(t *testing.T) {
	body := []byte(`
<span class="latest_update_description">A thrilling scene description here.</span>
<span class="update_tags">
	<a href="/tags/milf.html">MILF</a>
	<a href="/tags/pov.html">POV</a>
	<a href="/tags/taboo.html">Taboo</a>
</span>
`)
	d := parseDetailPage(body)
	if d.description != "A thrilling scene description here." {
		t.Errorf("description = %q", d.description)
	}
	if len(d.tags) != 3 || d.tags[0] != "MILF" || d.tags[2] != "Taboo" {
		t.Errorf("tags = %v", d.tags)
	}
}

func TestParseDetailPageEmpty(t *testing.T) {
	body := []byte(`<div>no description or tags</div>`)
	d := parseDetailPage(body)
	if d.description != "" {
		t.Errorf("description = %q", d.description)
	}
	if len(d.tags) != 0 {
		t.Errorf("tags = %v", d.tags)
	}
}

func TestEstimateTotal(t *testing.T) {
	body := []byte(`<a href="/categories/movies_2.html">2</a><a href="/categories/movies_5.html">5</a>`)
	if got := estimateTotal(body, 10); got != 50 {
		t.Errorf("estimateTotal = %d, want 50", got)
	}
	if got := estimateTotal([]byte(`no pages`), 10); got != 10 {
		t.Errorf("estimateTotal no pages = %d, want 10", got)
	}
}

// The site links its pages with a sort suffix (movies_2_d.html), which the
// scraper never requests but which is the only place a page count is published.
// Missing them made every run report a single page: a 343-scene catalogue was
// announced as 10.
func TestEstimateTotalReadsSuffixedPaginationLinks(t *testing.T) {
	body := []byte(`<div class="global_pagination">
		<a href="/categories/movies_1_d.html">1</a>
		<a href="/categories/movies_2_d.html">2</a>
		<a href="/categories/movies_35_d.html">35</a>
		<a href="/categories/movies_1_z.html">A-Z</a>
	</div>`)
	if got := estimateTotal(body, 10); got != 350 {
		t.Errorf("estimateTotal = %d, want 350 (35 pages x 10)", got)
	}
}

const listingTpl = `%s
%s`

const itemTpl = `<div class="updateItem">
	<a href="%s/updates/%s.html"><img src="content/%s/1.jpg" /></a>
	<div class="updateDetails">
		<div class="cart_buttons"><a href="#"><div class="buy_button">Buy $5.99</div></a></div>
		<h4><a href="%s/updates/%s.html">%s</a></h4>
		<p><span>01/01/2026</span></p>
	</div>
</div>`

const detailTpl = `
<span class="latest_update_description">Test description.</span>
<span class="update_tags"><a href="#">MILF</a></span>
`

func buildListingPage(base string, slugs []string, nextPages string) []byte {
	var items string
	for _, slug := range slugs {
		items += fmt.Sprintf(itemTpl, base, slug, slug, base, slug, slug)
	}
	return []byte(fmt.Sprintf(listingTpl, nextPages, items))
}

func newTestServer(pages [][]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")

		switch r.URL.Path {
		case "/categories/movies.html":
			nextPages := ""
			for p := 2; p <= len(pages); p++ {
				nextPages += fmt.Sprintf(`<a href="/categories/movies_%d.html">%d</a>`, p, p)
			}
			_, _ = w.Write(buildListingPage("", pages[0], nextPages))
		default:
			if len(r.URL.Path) > len("/categories/movies_") && r.URL.Path[:len("/categories/movies_")] == "/categories/movies_" {
				pageNum := 0
				_, _ = fmt.Sscanf(r.URL.Path, "/categories/movies_%d.html", &pageNum)
				idx := pageNum - 1
				if idx >= 0 && idx < len(pages) {
					_, _ = w.Write(buildListingPage("", pages[idx], ""))
				} else {
					_, _ = fmt.Fprint(w, `<div>empty</div>`)
				}
				return
			}
			_, _ = fmt.Fprint(w, detailTpl)
		}
	}))
}

func TestListScenes(t *testing.T) {
	ts := newTestServer([][]string{{"Scene-A", "Scene-B"}})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	studioURL := ts.URL + "/studio/"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
	for _, sc := range results {
		if sc.StudioURL != studioURL {
			t.Errorf("StudioURL = %q, want %q", sc.StudioURL, studioURL)
		}
	}
	if results[0].Description != "Test description." && results[1].Description != "Test description." {
		t.Error("expected description from detail page")
	}
}

func TestListScenesPagination(t *testing.T) {
	page1 := make([]string, 10)
	for i := range page1 {
		page1[i] = fmt.Sprintf("Scene-%d", i+1)
	}
	page2 := []string{"Scene-11", "Scene-12"}

	ts := newTestServer([][]string{page1, page2})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/categories/movies.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 12 {
		t.Fatalf("got %d scenes, want 12", len(results))
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	ts := newTestServer([][]string{{"A", "B", "C", "D"}})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/categories/movies.html", scraper.ListOpts{
		KnownIDs: map[string]bool{"C": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
}
