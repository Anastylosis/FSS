package carnalplus

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// ---- VariantNATS fixture (funsizeboys-style) ----

const natsListingHTML = `<html><body>
<div class="row videoBlock">

<div class="col-12 col-xs-6 col-sm-6 col-md-6 col-lg-4 col-xl-4 sceneTourBase" data-setid="8458" >
  <a href="https://funsizeboys.com/videos/workday-distraction-kai-vol-1.html" class="control_thumb">
    <div class="test thumbVid">
      <video class="img-fluid video-preview" poster="https://imagecdn.example/contentthumbs/25/77/72577-1x.jpg" muted loop>
        <source src="https://secure-cloud-cdn.example/fsb0203/thumbclip/thumbclip.mp4" type="video/mp4">
      </video>
      <div class="updateInfos">
        <div class="updateSite"><img src="https://cdn.example/minilogo/funsizeboys.png" alt="funsizeboys minilogo"></div>
        <div class="updateDetails">
          <h4>kai vol. 1 | workday distraction &amp; more</h4>
          <span><div>Starring Kai Neolani and Legrand Wolf</div></span>
        </div>
      </div>
    </div>
  </a>
</div>

<div class="col-12 col-xs-6 col-sm-6 col-md-6 col-lg-4 col-xl-4 sceneTourBase" data-setid="8450" >
  <a href="https://funsizeboys.com/videos/little-guy.html" class="control_thumb">
    <div class="test thumbVid">
      <video poster="https://imagecdn.example/contentthumbs/24/58/72458-1x.jpg">
        <source src="https://secure-cloud-cdn.example/fsb0202/thumbclip/thumbclip.mp4">
      </video>
      <div class="updateInfos">
        <div class="updateDetails">
          <h4>little guy big room</h4>
          <span><div>Starring Milo Eros, Sage Reeves &amp; Joe Doe</div></span>
        </div>
      </div>
    </div>
  </a>
</div>

</div>

<a href="movies_2_d.html">2</a>
<a href="movies_5_d.html">5</a>
</body></html>`

// ---- VariantGrid fixture (carnalplus.com style) ----

const gridListingHTML = `<html><body>
<div class="grid-item-eight ">
  <div class="swiper-slide">
    <a title="Watch caught-scout-marcus-vol-2"
       href="https://carnalplus.com/videos/caught-scout-marcus-vol-2.html"
       class="control_thumb">
      <picture data-iesrc="https://imagecdn.example/contentthumbs/25/91/72591-1x.jpg">
        <img loading="lazy" class="img-fluid"
             data-video-src="https://secure-cloud-cdn.example/sbs0152/thumbclip/thumbclip.mp4"
             src="https://imagecdn.example/contentthumbs/25/91/72591-1x.jpg" alt="">
      </picture>
    </a>
    <div class="updateInfos">
      <div class="updateSite">
        <a href="scoutboys/">
          <img src="https://cdn.example/minilogo/scoutboys.png" alt="scoutboys minilogo">
        </a>
      </div>
      <div class="updateDetails">
        <h4 class="capitalize titleBlockF">
          <span class='update-series'>Scout marcus vol. 2</span> |
          <span class='update-title'>caught by the scoutmasters</span>
        </h4>
        <div class='update-sitename'>
          Starring <a href="x">Marcus Daniels</a>, <a href="y">Bishop Angus</a>
        </div>
      </div>
    </div>
  </div>
</div>

<div class="grid-item-eight">
  <a href="https://carnalplus.com/videos/altar-boy-dex-vol-2.html" class="control_thumb">
    <picture>
      <img src="https://imagecdn.example/contentthumbs/26/04/72604-1x.jpg"
           class="img-fluid" data-video-src="https://secure-cloud-cdn.example/cb0203/thumbclip/thumbclip.mp4" alt="">
    </picture>
  </a>
  <div class="updateInfos">
    <div class="updateSite">
      <img src="https://cdn.example/minilogo/catholicboys.png" alt="catholicboys minilogo">
    </div>
    <div class="updateDetails">
      <h4>
        <span class='update-series'>Altar boy dex vol. 2</span> |
        <span class='update-title'>altar boy training</span>
      </h4>
    </div>
  </div>
</div>
</body></html>`

// ---- VariantWordPress fixture (growlboys style) ----

const wpListingJSON = `[
  {
    "id": 17,
    "date_gmt": "2026-05-20T01:18:21",
    "slug": "head-games",
    "link": "https://growlboys.com/head-games/",
    "title": {"rendered": "HEAD GAMES"},
    "content": {"rendered": "<p>A growl scene description.</p>"},
    "_embedded": {
      "wp:featuredmedia": [{"source_url": "https://growlboys.com/wp-content/thumb.webp"}],
      "wp:term": [
        [{"name": "Uncategorized", "taxonomy": "category"}],
        [{"name": "Performer A", "taxonomy": "post_tag"}, {"name": "Performer B", "taxonomy": "post_tag"}]
      ]
    }
  },
  {
    "id": 15,
    "date_gmt": "2026-04-10T00:00:00",
    "slug": "second-scene",
    "link": "https://growlboys.com/second-scene/",
    "title": {"rendered": "Second Scene"},
    "content": {"rendered": ""},
    "_embedded": {}
  }
]`

func TestParseNATSListing(t *testing.T) {
	items := parseNATSListing([]byte(natsListingHTML))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	first := items[0]
	if first.id != "8458" {
		t.Errorf("NATS ID = %q", first.id)
	}
	if first.title != "kai vol. 1 | workday distraction & more" {
		t.Errorf("NATS title = %q (entity unescape failed?)", first.title)
	}
	if first.url != "https://funsizeboys.com/videos/workday-distraction-kai-vol-1.html" {
		t.Errorf("NATS URL = %q", first.url)
	}
	if first.thumb != "https://imagecdn.example/contentthumbs/25/77/72577-1x.jpg" {
		t.Errorf("NATS thumb = %q", first.thumb)
	}
	if first.preview != "https://secure-cloud-cdn.example/fsb0203/thumbclip/thumbclip.mp4" {
		t.Errorf("NATS preview = %q", first.preview)
	}
	if len(first.performers) != 2 || first.performers[0] != "Kai Neolani" || first.performers[1] != "Legrand Wolf" {
		t.Errorf("NATS performers = %v", first.performers)
	}

	// Second card: 3 performers via "comma + ampersand" split.
	second := items[1]
	if len(second.performers) != 3 {
		t.Errorf("NATS Second performers = %v (want 3)", second.performers)
	}
}

func TestParseGridListing(t *testing.T) {
	items := parseGridListing([]byte(gridListingHTML))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	first := items[0]
	if first.id != "72591" {
		t.Errorf("Grid ID = %q (expected 72591 from contentthumbs/25/91/72591-1x)", first.id)
	}
	if first.url != "https://carnalplus.com/videos/caught-scout-marcus-vol-2.html" {
		t.Errorf("Grid URL = %q", first.url)
	}
	if first.title != "caught by the scoutmasters" {
		t.Errorf("Grid title = %q", first.title)
	}
	if first.series != "Scout Boys" {
		t.Errorf("Grid series = %q (expected human-readable subsite name from alt)", first.series)
	}
	if first.preview != "https://secure-cloud-cdn.example/sbs0152/thumbclip/thumbclip.mp4" {
		t.Errorf("Grid preview = %q", first.preview)
	}

	second := items[1]
	if second.id != "72604" {
		t.Errorf("Grid Second ID = %q", second.id)
	}
	if second.series != "Catholic Boys" {
		t.Errorf("Grid Second series = %q", second.series)
	}
}

func TestParseWPListing(t *testing.T) {
	h := http.Header{}
	h.Set("X-WP-Total", "44")
	h.Set("X-WP-TotalPages", "1")
	items, total, totalPages := parseWPListing([]byte(wpListingJSON), h)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if total != 44 {
		t.Errorf("WP total = %d, want 44", total)
	}
	if totalPages != 1 {
		t.Errorf("WP totalPages = %d, want 1", totalPages)
	}
	first := items[0]
	if first.id != "17" {
		t.Errorf("WP ID = %q", first.id)
	}
	if first.title != "HEAD GAMES" {
		t.Errorf("WP title = %q", first.title)
	}
	if first.url != "https://growlboys.com/head-games/" {
		t.Errorf("WP URL = %q", first.url)
	}
	if first.thumb != "https://growlboys.com/wp-content/thumb.webp" {
		t.Errorf("WP thumb = %q", first.thumb)
	}
	if len(first.performers) != 2 {
		t.Errorf("WP performers = %v", first.performers)
	}
	if first.date.Year() != 2026 || first.date.Month() != 5 || first.date.Day() != 20 {
		t.Errorf("WP date = %v", first.date)
	}
}

func TestSplitPerformers(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Kai Neolani and Legrand Wolf", []string{"Kai Neolani", "Legrand Wolf"}},
		{"A, B, C", []string{"A", "B", "C"}},
		{"A, B &amp; C", []string{"A", "B", "C"}},
		{"  ", nil},
	}
	for _, c := range cases {
		got := splitPerformers(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitPerformers(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitPerformers(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestListingURL(t *testing.T) {
	// NATS: every page is /categories/movies_N_d.html — including page 1,
	// which used to be the homepage and lost half of movies_1 to featured cards.
	nats := New(SiteConfig{ID: "x", SiteBase: "https://x.example", Variant: VariantNATS})
	if got := nats.listingURL(1, ""); got != "https://x.example/categories/movies_1_d.html" {
		t.Errorf("NATS page 1 → %q", got)
	}
	if got := nats.listingURL(3, ""); got != "https://x.example/categories/movies_3_d.html" {
		t.Errorf("NATS page 3 → %q", got)
	}
	// Grid root: page 1 → "/" ; page N → /?page=N.
	grid := New(SiteConfig{ID: "p", SiteBase: "https://carnalplus.com", Variant: VariantGrid})
	if got := grid.listingURL(1, ""); got != "https://carnalplus.com/" {
		t.Errorf("Grid root page 1 → %q", got)
	}
	if got := grid.listingURL(2, ""); got != "https://carnalplus.com/?page=2" {
		t.Errorf("Grid root page 2 → %q", got)
	}
	// Grid sub-path: page 1 → /baptistboys/ ; page N → /baptistboys/?page=N.
	sub := New(SiteConfig{
		ID: "bb", SiteBase: "https://carnalplus.com", SubPath: "/baptistboys",
		Variant: VariantGrid,
	})
	if got := sub.listingURL(1, ""); got != "https://carnalplus.com/baptistboys/" {
		t.Errorf("Grid sub page 1 → %q", got)
	}
	if got := sub.listingURL(2, ""); got != "https://carnalplus.com/baptistboys/?page=2" {
		t.Errorf("Grid sub page 2 → %q", got)
	}
	// WP.
	wp := New(SiteConfig{ID: "gb", SiteBase: "https://growlboys.com", Variant: VariantWordPress})
	if got := wp.listingURL(2, ""); got != "https://growlboys.com/wp-json/wp/v2/posts?per_page=100&_embed=1&page=2" {
		t.Errorf("WP page 2 → %q", got)
	}
}

func TestMatchesURL(t *testing.T) {
	get := func(id string) *Scraper {
		for _, cfg := range sites {
			if cfg.ID == id {
				return New(cfg)
			}
		}
		return nil
	}
	cases := []struct {
		scraperID, url string
		want           bool
	}{
		// NATS site
		{"funsizeboys", "https://funsizeboys.com/", true},
		{"funsizeboys", "https://www.funsizeboys.com/videos/x.html", true},
		{"funsizeboys", "https://other.com/", false},
		// Parent (only root + ?page=, NOT sub-paths)
		{"carnalplus", "https://carnalplus.com/", true},
		{"carnalplus", "https://carnalplus.com/?page=2", true},
		{"carnalplus", "https://carnalplus.com/baptistboys/", false},
		{"carnalplus", "https://carnalplus.com/carnaloriginals/", false},
		// Sub-brand
		{"baptistboys", "https://carnalplus.com/baptistboys/", true},
		{"baptistboys", "https://carnalplus.com/baptistboys/?page=2", true},
		{"baptistboys", "https://carnalplus.com/", false},
		// WP
		{"growlboys", "https://growlboys.com/", true},
		// Jockpack brands
		{"cumdumpsluts", "https://cumdumpsluts.com/", true},
		{"twinkloads", "https://www.twinkloads.com/categories/movies_2_d.html", true},
		{"twinkloads", "https://twinks.com/", false},
		{"jockpack", "https://jockpack.com/", true},
		{"jockpack", "https://jockpack.com/videos?page=2", true},
		{"jockpack", "https://jockpack.com/videos?site=hungfuckers", true},
		{"jockpack", "https://jockpack.com/videos?site=betabetapi", false},
		{"betabetapi", "https://jockpack.com/videos?site=betabetapi&page=2", true},
		{"betabetapi", "https://jockpack.com/videos?site=studloads", false},
		{"studloads", "https://jockpack.com/videos?site=studloads", true},
		{"jasonsparkslive", "https://jockpack.com/videos?page=2&site=jasonsparkslive", true},
		{"jasonsparkslive", "https://jockpack.com/", false},
	}
	for _, c := range cases {
		s := get(c.scraperID)
		if s == nil {
			t.Fatalf("unknown scraper ID %q", c.scraperID)
		}
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL[%s](%q) = %v, want %v", c.scraperID, c.url, got, c.want)
		}
	}
}

func TestSitesTable(t *testing.T) {
	seen := map[string]bool{}
	for _, cfg := range sites {
		if seen[cfg.ID] {
			t.Errorf("duplicate ID: %q", cfg.ID)
		}
		seen[cfg.ID] = true
	}
	if len(sites) != 28 {
		t.Errorf("expected 28 sites, got %d", len(sites))
	}
}

func TestListScenes_NATSendToEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/categories/movies_1_d.html":
			_, _ = fmt.Fprint(w, natsListingHTML)
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID: "funsizeboys", SiteBase: ts.URL, SiteName: "Fun-Size Boys",
		Variant: VariantNATS,
		MatchRe: regexp.MustCompile(`.*`),
	})
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var scenes int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			scenes++
			if r.Scene.Studio != "Carnal+" {
				t.Errorf("Studio = %q", r.Scene.Studio)
			}
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

func TestListScenes_WordPressEndToEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		page := r.URL.Query().Get("page")
		if page == "1" {
			w.Header().Set("X-WP-Total", "2")
			w.Header().Set("X-WP-TotalPages", "1")
			_, _ = fmt.Fprint(w, wpListingJSON)
		} else {
			_, _ = fmt.Fprint(w, `[]`)
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID: "growlboys", SiteBase: ts.URL, SiteName: "GrowlBoys",
		Variant: VariantWordPress,
		MatchRe: regexp.MustCompile(`.*`),
	})
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var scenes int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			scenes++
			if r.Scene.Series != "GrowlBoys" {
				t.Errorf("Series = %q (fallback to SiteName failed?)", r.Scene.Series)
			}
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

// The portal mixes scene cards, which split the heading into
// `update-series | update-title` spans, with full-movie cards that write the
// name straight into the heading. The latter used to arrive with no Title.
func TestParseGridListingFullMovieCard(t *testing.T) {
	const fullMovieHTML = `<html><body>
<div class="grid-item-eight ">
  <a title="Watch the-big-boss-vol-1-full-movie" href="https://carnalplus.com/videos/the-big-boss-vol-1-full-movie.html" class="control_thumb">
    <picture data-iesrc="https://imagecdn.carnalplus.com/imgsize//members/content/contentthumbs/30/05/73005-1x.jpg">
      <img loading="lazy" class="img-fluid" data-video-src="https://cdn.example/clip.mp4" src="https://imagecdn.carnalplus.com/imgsize//members/content/contentthumbs/30/05/73005-1x.jpg" alt="">
    </picture>
  </a>
  <div class="updateInfos">
    <div class="updateSite"><a href="carnaloriginals/"><img src="x" alt="carnaloriginals minilogo"></a></div>
    <div class="updateDetails"><div class="titleTruncate interested-list">
      <a href="https://carnalplus.com/videos/the-big-boss-vol-1-full-movie.html" class="control_thumb">
        <h4 class="capitalize titleBlockF">The Big Boss Vol. 1 Full Movie</h4>
      </a>
    </div></div>
  </div>
</div>
</body></html>`

	items := parseGridListing([]byte(fullMovieHTML))
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].title != "The Big Boss Vol. 1 Full Movie" {
		t.Errorf("title = %q", items[0].title)
	}
	if items[0].id != "73005" {
		t.Errorf("id = %q", items[0].id)
	}
}

// Page 1 must be the categories listing, not the homepage: the homepage's
// second half is years-old featured scenes, so using it lost half of movies_1
// permanently and stopped incremental runs after six new scenes.
func TestNATSListingURLStartsAtMoviesPage1(t *testing.T) {
	s := New(SiteConfig{ID: "test", SiteBase: "https://example.test", Variant: VariantNATS})
	cases := map[int]string{
		1: "https://example.test/categories/movies_1_d.html",
		2: "https://example.test/categories/movies_2_d.html",
	}
	for page, want := range cases {
		if got := s.listingURL(page, ""); got != want {
			t.Errorf("listingURL(%d) = %q, want %q", page, got, want)
		}
	}
}

// The grid listing answers ?page=N from a rotating pool and never returns an
// empty page, so the walk has to bound itself or --full runs forever.
func TestGridWalkTerminatesOnRepeats(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/videos/") {
			_, _ = fmt.Fprint(w, "<html></html>")
			return
		}
		pages++
		// Page 1 is fresh; every page after it repeats those same cards.
		_, _ = fmt.Fprint(w, gridListingHTML)
	}))
	defer srv.Close()

	s := New(SiteConfig{ID: "test", SiteBase: srv.URL, Variant: VariantGrid})
	s.client = srv.Client()

	out := make(chan scraper.SceneResult, 64)
	done := make(chan struct{})
	var scenes []string
	go func() {
		defer close(done)
		for r := range out {
			if r.Kind == scraper.KindScene {
				scenes = append(scenes, r.Scene.ID)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s.run(ctx, srv.URL+"/", scraper.ListOpts{}, out)
	<-done

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want the 2 distinct cards emitted once each: %v", len(scenes), scenes)
	}
	// One fresh page plus gridDryPages repeats, and no more.
	if want := 1 + gridDryPages; pages != want {
		t.Errorf("fetched %d pages, want %d (stop after %d dry pages)", pages, want, gridDryPages)
	}
}

func TestJockpackListingURL(t *testing.T) {
	get := func(id string) *Scraper {
		for _, cfg := range sites {
			if cfg.ID == id {
				return New(cfg)
			}
		}
		t.Fatalf("no site %q", id)
		return nil
	}
	cases := []struct {
		id, studioURL string
		page          int
		want          string
	}{
		{"jockpack", "https://jockpack.com/", 1, "https://jockpack.com/videos"},
		{"jockpack", "https://jockpack.com/", 3, "https://jockpack.com/videos?page=3"},
		{"jockpack", "https://jockpack.com/videos?site=hungfuckers", 2, "https://jockpack.com/videos?page=2&site=hungfuckers"},
		{"betabetapi", "https://jockpack.com/videos?site=betabetapi", 1, "https://jockpack.com/videos?site=betabetapi"},
		{"jasonsparkslive", "https://jockpack.com/videos?site=jasonsparkslive", 7, "https://jockpack.com/videos?page=7&site=jasonsparkslive"},
	}
	for _, c := range cases {
		if got := get(c.id).listingURL(c.page, c.studioURL); got != c.want {
			t.Errorf("%s page %d = %q, want %q", c.id, c.page, got, c.want)
		}
	}
}

func TestJockpackListScenes(t *testing.T) {
	fixture, err := os.ReadFile("testdata/jockpack_list.html")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := os.ReadFile("testdata/jockpack_detail.html")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu      sync.Mutex
		queries []string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		if r.URL.Path == "/videos" && r.URL.Query().Get("page") == "" {
			_, _ = w.Write(fixture)
			return
		}
		if r.URL.Path == "/videos/perks-of-brotherhood-pledge-oliver-vol-1.html" {
			_, _ = w.Write(detail)
			return
		}
		_, _ = fmt.Fprint(w, "<html></html>")
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID: "betabetapi", SiteBase: ts.URL, ListPath: "/videos", SiteFilter: "betabetapi",
		SiteName: "BetaBetaPi", Variant: VariantGrid, MatchRe: regexp.MustCompile(`.*`),
	})
	studioURL := ts.URL + "/videos?site=betabetapi"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var (
		scenes []models.Scene
		errs   []error
	)
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	got := scenes[0]
	if got.Date.Format("2006-01-02") != "2026-10-26" || got.Duration != 24*60+14 {
		t.Errorf("date/duration = %v / %d", got.Date, got.Duration)
	}
	if len(got.Performers) != 2 || got.Director != "Brian Fitzgibbons" || len(got.Tags) != 13 || got.Description == "" {
		t.Errorf("detail fields = %+v", got)
	}
	// The second card's detail page carries nothing: the error is reported and
	// the listing scene is still emitted, unchanged.
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Errorf("errors = %v, want one parse error", errs)
	}
	if !scenes[1].Date.IsZero() || scenes[1].ID == "" {
		t.Errorf("second scene = %+v", scenes[1])
	}
	if got.ID != "86262" || got.Title != "perks of brotherhood" || got.Series != "BetaBetaPi" {
		t.Errorf("scene = %+v", got)
	}
	if got.URL != "https://jockpack.com/videos/perks-of-brotherhood-pledge-oliver-vol-1.html" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.StudioURL != studioURL || got.SiteID != "betabetapi" || got.Thumbnail == "" {
		t.Errorf("scene = %+v", got)
	}
	if len(queries) == 0 || queries[0] != "site=betabetapi" {
		t.Errorf("first query = %v", queries)
	}
}

func TestParseNATSListingNestedTitle(t *testing.T) {
	const page = `<div class="col-12 sceneTourBase" data-setid="2885" >
<a  href="https://twinkloads.com/videos/x.html" class="control_thumb"></a>
<div class="updateDetails"><h4>connor taylor <span class="chapterNumber ">Encounter 2</span> <span class="sceneTitle">braden rush bottoms</span></h4>
<span><div>Starring Braden Rush and Connor Taylor</div></span></div></div>`
	items := parseNATSListing([]byte(page))
	if len(items) != 1 || items[0].title != "braden rush bottoms" {
		t.Fatalf("items = %+v", items)
	}
	if len(items[0].performers) != 2 || items[0].performers[1] != "Connor Taylor" {
		t.Errorf("performers = %v", items[0].performers)
	}
}
