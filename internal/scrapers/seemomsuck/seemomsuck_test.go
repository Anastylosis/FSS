package seemomsuck

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

// listingCard renders one /videos listing card. performers is a
// comma-separated "Name" list; an empty string produces a performer-less
// card (the site really has these — compilation reels have no attributed
// performer).
func listingCard(id, title, performers, desc, thumbSlug string) string {
	var perfHTML string
	if performers != "" {
		var links []string
		for _, p := range strings.Split(performers, ", ") {
			slug := strings.ToLower(strings.ReplaceAll(p, " ", "-"))
			links = append(links, fmt.Sprintf(`<a class="text-white underline hover:text-primary" href="https://www.seemomsuck.com/models/%s">%s</a>`, slug, p))
		}
		perfHTML = fmt.Sprintf(`<p class="text-base md:text-lg flex flex-wrap gap-2 mt-1">%s</p>`, strings.Join(links, " "))
	}
	return fmt.Sprintf(`<div class="flex flex-col md:flex-row md:items-start gap-6 lg:gap-10 mb-10 md:mb-12 py-2 md:py-4 lg:py-6">
  <div class="w-full md:w-1/2 md:max-w-[50%%] mb-4 md:mb-0 shrink-0">
    <a href="https://www.seemomsuck.com/video/%s">
      <img src="https://nookies.com/tour-content/seemomsuck/%s/main.jpg" alt="%s" />
    </a>
  </div>
  <div class="w-full md:w-1/2 md:min-w-0 flex flex-col md:px-2 lg:px-6 xl:px-8">
    <h2 class="text-xl md:text-2xl lg:text-3xl font-bold uppercase tracking-wide leading-tight">
      <a href="https://www.seemomsuck.com/video/%s">%s</a>
    </h2>
    <p class="mt-2 md:mt-3 text-gray-400 text-base md:text-lg">
      Published Feb 05, 2025 • 14,474 views
    </p>
    %s
    <p class="mt-4 mb-4 md:mb-6 leading-relaxed text-base md:text-lg text-gray-200">
        %s
    </p>
    <a href="https://www.seemomsuck.com/video/%s" class="mt-6 md:mt-8 block ...">WATCH TRAILER</a>
  </div>
</div>`, id, thumbSlug, title, id, title, perfHTML, desc, id)
}

func paginationNav(current, maxPage int) string {
	var links []string
	for p := 1; p <= maxPage; p++ {
		if p == current {
			continue
		}
		links = append(links, fmt.Sprintf(`<a href="https://www.seemomsuck.com/videos?sort=date&amp;page=%d">%d</a>`, p, p))
	}
	return fmt.Sprintf(`<nav aria-label="Pagination">%s</nav>`, strings.Join(links, "\n"))
}

const realListingFixture = `<div class="flex flex-col md:flex-row md:items-start gap-6 lg:gap-10 mb-10 md:mb-12 py-2 md:py-4 lg:py-6">
          <div class="w-full md:w-1/2 md:max-w-[50%] mb-4 md:mb-0 shrink-0">
            <a href="https://www.seemomsuck.com/video/3105">
              <img
                src="https://nookies.com/tour-content/seemomsuck/Morgan-ShipleyBJ-sms/main.jpg"
                alt="In the Mood For Morning Wood: Morgan Shipley"
                width="655"
                height="437"
                class="w-full h-auto object-cover rounded-lg"
              />
            </a>
          </div>
          <div class="w-full md:w-1/2 md:min-w-0 flex flex-col md:px-2 lg:px-6 xl:px-8">
            <h2 class="text-xl md:text-2xl lg:text-3xl font-bold uppercase tracking-wide leading-tight">
              <a href="https://www.seemomsuck.com/video/3105">In the Mood For Morning Wood: Morgan Shipley</a>
            </h2>
            <p class="mt-2 md:mt-3 text-gray-400 text-base md:text-lg">
              Published Feb 05, 2025 • 14,474 views
            </p>
                          <p class="text-base md:text-lg flex flex-wrap gap-2 mt-1">
                                  <a class="text-white underline hover:text-primary" href="https://www.seemomsuck.com/models/morgan-shipley">
                    Morgan Shipley
                  </a>
                              </p>

            <p class="mt-4 mb-4 md:mb-6 leading-relaxed text-base md:text-lg text-gray-200">
                Morgan Shipley noticed that Blake has a morning erection and she can&#039;t let him leave the house like that.
            </p>

            <a
              href="https://www.seemomsuck.com/video/3105"
              class="mt-6 md:mt-8 block bg-[#67c0cb] hover:bg-black/80 transition duration-300 text-white hover:text-[#67c0cb] w-full py-3 px-6 rounded-full text-lg md:text-xl lg:text-2xl font-bold text-center uppercase tracking-wider border-2 border-[#67c0cb]"
            >
              WATCH TRAILER
            </a>
          </div>
        </div>                  <div class="flex flex-col md:flex-row md:items-start gap-6 lg:gap-10 mb-10 md:mb-12 py-2 md:py-4 lg:py-6">
          <div class="w-full md:w-1/2 md:max-w-[50%] mb-4 md:mb-0 shrink-0">
            <a href="https://www.seemomsuck.com/video/3014">
              <img
                src="https://nookies.com/tour-content/seemomsuck/compilation-SeeMom/main.jpg"
                alt="See Mom Suck Top Cumshots Compilation!"
                width="655"
                height="437"
                class="w-full h-auto object-cover rounded-lg"
              />
            </a>
          </div>
          <div class="w-full md:w-1/2 md:min-w-0 flex flex-col md:px-2 lg:px-6 xl:px-8">
            <h2 class="text-xl md:text-2xl lg:text-3xl font-bold uppercase tracking-wide leading-tight">
              <a href="https://www.seemomsuck.com/video/3014">See Mom Suck Top Cumshots Compilation!</a>
            </h2>
            <p class="mt-2 md:mt-3 text-gray-400 text-base md:text-lg">
              Published Oct 18, 2024 • 15,334 views
            </p>

            <p class="mt-4 mb-4 md:mb-6 leading-relaxed text-base md:text-lg text-gray-200">
                Check out our hot new compilation reel of our top-ranked cumshots.
            </p>

            <a
              href="https://www.seemomsuck.com/video/3014"
              class="mt-6 md:mt-8 block bg-[#67c0cb] hover:bg-black/80 transition duration-300 text-white hover:text-[#67c0cb] w-full py-3 px-6 rounded-full text-lg md:text-xl lg:text-2xl font-bold text-center uppercase tracking-wider border-2 border-[#67c0cb]"
            >
              WATCH TRAILER
            </a>
          </div>
        </div>`

func TestParseListingCardsRealFixture(t *testing.T) {
	scenes := parseListingCards([]byte(realListingFixture))
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s0 := scenes[0]
	if s0.id != "3105" {
		t.Errorf("id = %q", s0.id)
	}
	if s0.title != "In the Mood For Morning Wood: Morgan Shipley" {
		t.Errorf("title = %q", s0.title)
	}
	if len(s0.performers) != 1 || s0.performers[0] != "Morgan Shipley" {
		t.Errorf("performers = %v", s0.performers)
	}
	if !strings.Contains(s0.description, "can't let him leave the house") {
		t.Errorf("description = %q", s0.description)
	}
	if s0.thumb != "https://nookies.com/tour-content/seemomsuck/Morgan-ShipleyBJ-sms/main.jpg" {
		t.Errorf("thumb = %q", s0.thumb)
	}
	if s0.views != 14474 {
		t.Errorf("views = %d, want 14474", s0.views)
	}
	if s0.published.IsZero() || s0.published.Format("2006-01-02") != "2025-02-05" {
		t.Errorf("published = %v", s0.published)
	}

	// Compilation reel: no attributed performer. Must not be dropped or
	// crash the parser.
	s1 := scenes[1]
	if s1.id != "3014" {
		t.Errorf("id = %q", s1.id)
	}
	if len(s1.performers) != 0 {
		t.Errorf("performers = %v, want none", s1.performers)
	}
}

func TestParseListingCardsMultiPerformer(t *testing.T) {
	html := listingCard("2797", "Experienced Step Mom", "Abi James, Bunny Fae", "Desc.", "abi-bunny")
	scenes := parseListingCards([]byte(html))
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if len(scenes[0].performers) != 2 || scenes[0].performers[0] != "Abi James" || scenes[0].performers[1] != "Bunny Fae" {
		t.Errorf("performers = %v", scenes[0].performers)
	}
}

func TestExtractMaxPage(t *testing.T) {
	nav := paginationNav(1, 13)
	if got := extractMaxPage([]byte(nav)); got != 13 {
		t.Errorf("maxPage = %d, want 13", got)
	}
	if got := extractMaxPage([]byte("<div>no pagination here</div>")); got != 1 {
		t.Errorf("maxPage = %d, want 1 (default)", got)
	}
}

func TestExtractModelName(t *testing.T) {
	html := `<h1
                class="text-3xl lg:text-4xl font-bold text-white mb-8 tracking-wide uppercase"
              >
                Stacie Starr
              </h1>`
	if got := extractModelName([]byte(html)); got != "Stacie Starr" {
		t.Errorf("model name = %q, want %q", got, "Stacie Starr")
	}
}

// modelCard renders one /models/{slug} card (no description, no per-scene
// performer link — the page-level model name is the fallback performer).
func modelCard(id, title, thumbSlug string) string {
	return fmt.Sprintf(`<div class="group cursor-pointer hover-video"  data-preview="https://nookies.com/tour-content/seemomsuck/%s/preview.mp4" >
  <a href="https://www.seemomsuck.com/video/%s" class="block">
    <div class="relative overflow-hidden rounded-xl aspect-video mb-3 js-video-card">
      <img src="https://nookies.com/tour-content/seemomsuck/%s/main.jpg" alt="%s" class="w-full h-full object-cover" />
    </div>
    <h3 class="text-white font-semibold text-center">
      %s
    </h3>
  </a>
</div>`, thumbSlug, id, thumbSlug, title, title)
}

// crossPromoCard renders the "More Scenes With {Model}" cross-network promo
// card — same wrapper markup as modelCard, but it links to a signup page on
// a different site instead of a /video/{id} URL, so it must be skipped.
func crossPromoCard(thumbSlug, alt string) string {
	return fmt.Sprintf(`<div class="group cursor-pointer hover-video"  data-preview="https://nookies.com/tour-content/clubtug/%s/preview.mp4" >
  <a href="https://join.seemomsuck.com/signup/signup.php?nats=abc&amp;step=2" class="block">
    <div class="relative overflow-hidden rounded-xl aspect-video mb-3 js-video-card">
      <img src="https://nookies.com/tour-content/clubtug/%s/main.jpg" alt="%s" class="w-full h-full object-cover" />
    </div>
    <h3 class="text-white font-semibold text-center">
      %s
    </h3>
  </a>
</div>`, thumbSlug, thumbSlug, alt, alt)
}

func TestParseModelCards(t *testing.T) {
	html := modelCard("1928", "No Blowjobs Until You Turn 21! - Jan 09", "sms_staciestarr_raina") +
		modelCard("1891", "Step Moms Monster Cock - May 09", "stacie_starr_1") +
		crossPromoCard("shelly", "Shelly")

	scenes := parseModelCards([]byte(html))
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (cross-promo card must be skipped): %+v", len(scenes), scenes)
	}
	if scenes[0].id != "1928" || scenes[0].title != "No Blowjobs Until You Turn 21! - Jan 09" {
		t.Errorf("scene 0 = %+v", scenes[0])
	}
	if scenes[1].id != "1891" {
		t.Errorf("scene 1 = %+v", scenes[1])
	}
	for _, s := range scenes {
		if len(s.performers) != 0 {
			t.Errorf("model card scenes carry no per-scene performer, got %v", s.performers)
		}
		if s.thumb == "" {
			t.Errorf("scene %s missing thumb", s.id)
		}
	}
}

func TestToScene(t *testing.T) {
	ps := parsedScene{
		id:          "3105",
		title:       "Test Scene",
		performers:  []string{"Performer A"},
		description: "A description.",
		thumb:       "https://nookies.com/thumb.jpg",
		views:       100,
	}
	scene := ps.toScene("https://www.example.com", "https://www.example.com/videos", fixedTime)
	if scene.StudioURL != "https://www.example.com/videos" {
		t.Errorf("StudioURL = %q", scene.StudioURL)
	}
	if scene.ID != "3105" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "seemomsuck" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.URL != "https://www.example.com/video/3105" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Studio != "See Mom Suck" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if scene.Views != 100 {
		t.Errorf("Views = %d", scene.Views)
	}
	if !scene.Date.IsZero() {
		t.Errorf("Date should be zero when published is unset, got %v", scene.Date)
	}
}

var fixedTime = func() time.Time {
	t, _ := time.Parse(time.RFC3339, "2026-04-27T12:00:00Z")
	return t
}()

func TestCleanModelURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://example.com/models/stacie-starr.html?nats=abc123", "https://example.com/models/stacie-starr"},
		{"https://example.com/models/stacie-starr", "https://example.com/models/stacie-starr"},
		{"https://example.com/models/stacie-starr.html", "https://example.com/models/stacie-starr"},
		{"https://example.com/page?foo=bar&nats=abc", "https://example.com/page?foo=bar"},
	}
	for _, c := range cases {
		if got := cleanModelURL(c.in); got != c.want {
			t.Errorf("cleanModelURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.seemomsuck.com", true},
		{"https://seemomsuck.com/videos?sort=date", true},
		{"https://www.seemomsuck.com/models/stacie-starr", true},
		// The site's old pre-migration URL form (redirects live, but a
		// user's saved bookmark may still use it) must still match.
		{"https://www.seemomsuck.com/models/stacie-starr.html?nats=abc", true},
		{"https://example.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestRun(t *testing.T) {
	page1 := listingCard("100", "Scene A", "Performer A", "Desc A", "a") +
		listingCard("101", "Scene B", "Performer B", "Desc B", "b")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, page1)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	out := make(chan scraper.SceneResult)
	go s.run(context.Background(), ts.URL, scraper.ListOpts{}, out)

	scenes := testutil.CollectScenes(t, out)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
}

func TestRunPaginates(t *testing.T) {
	page1 := listingCard("200", "Scene A", "Performer A", "Desc A", "a") + paginationNav(1, 2)
	page2 := listingCard("199", "Scene B", "Performer B", "Desc B", "b") + paginationNav(2, 2)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			_, _ = fmt.Fprint(w, page2)
			return
		}
		_, _ = fmt.Fprint(w, page1)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	out := make(chan scraper.SceneResult)
	studioURL := ts.URL + "/videos"
	go s.run(context.Background(), studioURL, scraper.ListOpts{}, out)

	scenes := testutil.CollectScenes(t, out)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2: %+v", len(scenes), scenes)
	}
	for _, sc := range scenes {
		if sc.StudioURL != studioURL {
			t.Errorf("StudioURL = %q, want %q", sc.StudioURL, studioURL)
		}
	}
	if scenes[0].ID != "200" || scenes[1].ID != "199" {
		t.Errorf("scene order = [%s, %s]", scenes[0].ID, scenes[1].ID)
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	page1 := listingCard("300", "Scene A", "Performer A", "Desc A", "a") +
		listingCard("301", "Scene B", "Performer B", "Desc B", "b")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, page1)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	out := make(chan scraper.SceneResult)
	go s.run(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"301": true},
	}, out)

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, out)
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
	if len(scenes) != 1 || scenes[0].ID != "300" {
		t.Errorf("got %d scenes, want [300]", len(scenes))
	}
}

func TestModelURL(t *testing.T) {
	modelPage := `<h1
                class="text-3xl lg:text-4xl font-bold text-white mb-8 tracking-wide uppercase"
              >
                Test Model
              </h1>` +
		modelCard("400", "Model Scene A", "a") +
		modelCard("401", "Model Scene B", "b") +
		crossPromoCard("promo", "Promo")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, modelPage)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	modelURL := ts.URL + "/models/test-model"
	out := make(chan scraper.SceneResult)
	go s.run(context.Background(), modelURL, scraper.ListOpts{}, out)

	var scenes []scraper.SceneResult
	var total int
	for r := range out {
		if r.Kind == scraper.KindTotal {
			total = r.Total
			continue
		}
		if r.Kind == scraper.KindStoppedEarly {
			continue
		}
		scenes = append(scenes, r)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, r := range scenes {
		if r.Err != nil {
			t.Errorf("error: %v", r.Err)
			continue
		}
		if r.Scene.StudioURL != modelURL {
			t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, modelURL)
		}
		if len(r.Scene.Performers) != 1 || r.Scene.Performers[0] != "Test Model" {
			t.Errorf("performers = %v, want [Test Model]", r.Scene.Performers)
		}
	}
}

func TestModelURLOldHTMLSuffixStillWorks(t *testing.T) {
	modelPage := `<h1
                class="text-3xl lg:text-4xl font-bold text-white mb-8 tracking-wide uppercase"
              >
                Test Model
              </h1>` + modelCard("500", "Model Scene", "a")

	var requestedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		_, _ = fmt.Fprint(w, modelPage)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	modelURL := ts.URL + "/models/test-model.html?nats=abc"
	out := make(chan scraper.SceneResult)
	go s.run(context.Background(), modelURL, scraper.ListOpts{}, out)

	scenes := testutil.CollectScenes(t, out)
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if requestedPath != "/models/test-model" {
		t.Errorf("requested path = %q, want the .html-stripped canonical form", requestedPath)
	}
}

func TestCleanDescription(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Simple description.", "Simple description."},
		{"  Spaces around.  ", "Spaces around."},
		{"Line\none.", "Line one."},
		{"Entity &amp; test.", "Entity & test."},
	}
	for _, c := range cases {
		if got := cleanDescription(c.in); got != c.want {
			t.Errorf("cleanDescription(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestScraperInterface(t *testing.T) {
	var _ scraper.StudioScraper = New()
}
