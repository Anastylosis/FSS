package boundhoneys

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://boundhoneys.com/", "https://www.boundhoneys.com/bondage-videos.php", "http://boundhoneys.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://boundhoneys.net/", "https://notboundhoneys.com/", "https://example.com/boundhoneys.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestFilterPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://boundhoneys.com/", ""},
		{"https://boundhoneys.com/bondage-videos.php", ""},
		{"https://boundhoneys.com/bondage-girls.php", ""},
		{"https://boundhoneys.com/bondage-categories.php", ""},
		{"https://boundhoneys.com/alessandra-jane.php", "/alessandra-jane.php?perpage=9999"},
		{"https://boundhoneys.com/bondage-category/rope-bondage.php", "/bondage-category/rope-bondage.php?perpage=9999"},
		// A model page with the site's own control already on it.
		{"https://boundhoneys.com/alessandra-jane.php?perpage=12", "/alessandra-jane.php?perpage=9999"},
	}
	for _, c := range cases {
		if got := filterPath(c.in); got != c.want {
			t.Errorf("filterPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing.html"))
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	if first.id == "" || first.path != "bondage-video/"+first.id+".php" {
		t.Errorf("id/path = %q/%q", first.id, first.path)
	}
	if first.title == "" {
		t.Error("title missing")
	}
	if len(first.performers) == 0 {
		t.Error("performers missing")
	}
	if len(first.categories) == 0 {
		t.Error("categories missing")
	}
}

// The same video appears in more than one block on some pages.
func TestParseListingDeduplicates(t *testing.T) {
	card := `<div class='update'>
	<a href="bondage-video/x.php"><img src="images/smallpreview/x.jpg" /></a>
	<div class='updateTitle'><a href="bondage-video/x.php">X</a></div>
	</div>`
	if got := parseListing([]byte(card + card)); len(got) != 1 {
		t.Errorf("parseListing = %+v, want one", got)
	}
}

// The category list ends with a literal "..." when it is truncated; that is
// not a category.
func TestAnchorNamesDropsEllipsis(t *testing.T) {
	got := anchorNames([]byte(`<a href="a">Rope</a>, <a href="b">Gag</a>, ...`))
	if len(got) != 2 {
		t.Errorf("anchorNames = %v", got)
	}
}

func TestEnrichFromDetail(t *testing.T) {
	item := listItem{}
	enrichFromDetail(readFixture(t, "detail.html"), &item)
	if item.duration != 16*60 {
		t.Errorf("duration = %d, want %d", item.duration, 16*60)
	}
	if !strings.HasPrefix(item.description, "Alessandra Jane really wants") {
		t.Errorf("description = %q", item.description)
	}
}

func TestToScene(t *testing.T) {
	s := New()
	item := listItem{
		id: "x", path: "bondage-video/x.php", title: "T",
		thumbnail: "images/smallpreview/x.jpg", performers: []string{"A"},
		categories: []string{"Rope Bondage"}, duration: 960, description: "D",
	}
	sc := s.toScene(item, "https://boundhoneys.com/", time.Now().UTC())
	if sc.URL != "https://boundhoneys.com/bondage-video/x.php" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Thumbnail != "https://boundhoneys.com/images/smallpreview/x.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	// The site publishes no date anywhere, so it stays zero rather than being
	// guessed from the scrape time.
	if !sc.Date.IsZero() {
		t.Errorf("Date = %v, want zero", sc.Date)
	}
}

func TestListScenes(t *testing.T) {
	// The detail fetches run in a worker pool, so handler goroutines overlap
	// and the record of what was requested needs guarding.
	var (
		mu    sync.Mutex
		asked []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/bondage-video/") {
			_, _ = w.Write(readFixture(t, "detail.html"))
			return
		}
		_, _ = w.Write(readFixture(t, "listing.html"))
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://boundhoneys.com/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []models.Scene
	total := 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindTotal:
			total = res.Total
		case scraper.KindError:
			t.Errorf("error result: %v", res.Err)
		}
	}
	if len(scenes) != 2 || total != 2 {
		t.Fatalf("got %d scenes / total %d, want 2", len(scenes), total)
	}
	// One listing request, then one per scene: the catalogue is a single page.
	mu.Lock()
	defer mu.Unlock()
	listings := 0
	for _, p := range asked {
		if !strings.HasPrefix(p, "/bondage-video/") {
			listings++
		}
	}
	if listings != 1 {
		t.Errorf("listing requests = %d, want 1", listings)
	}
	for _, sc := range scenes {
		if sc.Duration == 0 || sc.Description == "" {
			t.Errorf("detail enrichment missing on %s", sc.ID)
		}
	}
}

// A listing that parses to nothing is a parse failure, not an empty catalogue.
func TestEmptyListingIsAParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>redesigned</body></html>"))
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://boundhoneys.com/", scraper.ListOpts{})
	errs := 0
	for res := range ch {
		if res.Kind == scraper.KindError {
			errs++
			if got := scraper.Classify(res.Err); got != scraper.FailureParse {
				t.Errorf("Classify = %v, want FailureParse", got)
			}
		}
	}
	if errs != 1 {
		t.Errorf("errors = %d, want 1", errs)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}
