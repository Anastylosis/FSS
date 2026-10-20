package heyzo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// card reproduces one listing tile, including the awkward parts: the class
// attribute is split across lines, the title lives on the <img> rather than in
// a heading, and the release date sits inside a <p class="release">.
func card(id, title, performer, date string) string {
	return fmt.Sprintf(`<div
 class="movie movie358 ppv-available"
    data-movie-id="%s"
  >
    <a href="/moviepages/%s/index.html">
      <img class="lazy" src="/images/common/parts/ajax-loader.gif"
        data-original="/contents/3000/%s/images/player_thumbnail_358.jpg"
        alt="%s"
        title="%s"
      />
    </a>
    <span list class="actor-type-mature icon">
      Mature <p class="release">Released: %s</p>
    </span>
    <a href="/listpages/actor_293_1.html?sort=pop" class="actor" title="">%s</a>
  </div>`, id, id, id, performer, title, date, performer)
}

// detailHTML carries both metadata sources: the schema.org Movie JSON-LD and
// the movieInfo table. The <script> block deliberately mentions the row class
// names, the way the live page's jQuery does — the parser has to strip scripts
// before it looks for the table, or it finds the selectors instead.
func detailHTML(title, desc, dur, date, actor string, cats, tags []string, series string) string {
	catCells := ""
	for _, c := range cats {
		catCells += fmt.Sprintf(`<span><a href="/listpages/category_22_1.html?sort=pop">%s</a></span>`, c)
	}
	tagCells := ""
	for _, tg := range tags {
		tagCells += fmt.Sprintf(`<li><a href="/search/%s/1.html?sort=pop">%s</a></li>`, tg, tg)
	}
	return fmt.Sprintf(`<html><head>
<script type="application/ld+json">
{"@context":"http://schema.org","@type":"Movie","name":%q,
 "image":"//www.heyzo.com/contents/3000/3400/images/player_thumbnail.jpg",
 "actor":{"@type":"Person","name":%q},
 "description":%q,"duration":%q,"dateCreated":%q,
 "video":{"@type":"VideoObject","name":"IGNORED INNER NAME","description":"IGNORED INNER DESC","duration":"PT9H9M9S"}}
</script>
<script>$('.table-tag-keyword-small').css('display','none'); $('.table-series').hide();</script>
</head><body>
<table class="movieInfo"><tbody>
<tr class="table-release-day"><td>Released</td><td>%s</td></tr>
<tr class="table-actor"><td>Cast</td><td><a href="/listpages/actor_293_1.html?sort=pop"><span>%s</span></a></td></tr>
<tr class="table-series"><td>Series</td><td>%s</td></tr>
<tr class="table-actor-type"><td>Type</td><td>%s</td></tr>
<tr class="table-tag-keyword-small"><td colspan="2">Tag keywords</td></tr>
<tr class="table-tag-keyword-small"><td colspan="2"><ul class="tag-keyword-list">%s</ul></td></tr>
<tr class="table-memo"><td colspan="2"><p class="memo">The site's own synopsis.</p></td></tr>
</tbody></table>
</body></html>`, title, actor, desc, dur, date, date, actor, series, catCells, tagCells)
}

type stubSite struct {
	*httptest.Server
	mu     sync.Mutex
	hits   map[string]int
	pages  int
	perPag int
}

func (s *stubSite) hit(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits[p]++
}

func (s *stubSite) count(p string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[p]
}

// catalogueServer serves `pages` listing pages of `perPage` cards, then answers
// 200 with an empty grid — which is what the live site does past the end,
// rather than 404ing.
func catalogueServer(t *testing.T, pages, perPage int) *stubSite {
	t.Helper()
	site := &stubSite{hits: map[string]int{}, pages: pages, perPag: perPage}
	site.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site.hit(r.URL.Path)
		switch {
		case strings.HasPrefix(r.URL.Path, "/listpages/"):
			name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/listpages/"), ".html")
			i := strings.LastIndex(name, "_")
			page, _ := strconv.Atoi(name[i+1:])
			if page < 1 || page > pages {
				_, _ = fmt.Fprint(w, `<html><body><div id="movies"><div class="movie-list"></div></div></body></html>`)
				return
			}
			var sb strings.Builder
			sb.WriteString(`<html><body><div class="movie-list">`)
			for n := 0; n < perPage; n++ {
				id := strconv.Itoa(1000 + (page-1)*perPage + n)
				sb.WriteString(card(id, "Title "+id, "Hester Prynne", "2026-08-1"+strconv.Itoa(n%10)))
			}
			sb.WriteString(`</div></body></html>`)
			_, _ = fmt.Fprint(w, sb.String())
		case strings.HasPrefix(r.URL.Path, "/moviepages/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/moviepages/"), "/index.html")
			_, _ = fmt.Fprint(w, detailHTML(
				"Detail Title "+id, "A description.", "PT0H58M54S", "2024-08-17", "Jane Eyre",
				[]string{"Mature", "Slim"}, []string{"69", "Toys", "69"}, "-----"))
		default:
			http.NotFound(w, r)
		}
	}))
	return site
}

func newTestScraper(srv *httptest.Server) *Scraper {
	s := New()
	s.Client = srv.Client()
	s.baseOverride = srv.URL
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, []error, int, bool) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []models.Scene
	var errs []error
	total := 0
	stopped := false
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindError:
			errs = append(errs, res.Err)
		case scraper.KindTotal:
			total = res.Total
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return scenes, errs, total, stopped
}

func TestWalksEveryListingPage(t *testing.T) {
	site := catalogueServer(t, 3, 4)
	defer site.Close()

	s := newTestScraper(site.Server)
	scenes, errs, total, _ := collect(t, s, "https://www.heyzo.com/", scraper.ListOpts{Workers: 2})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 12 || total != 12 {
		t.Fatalf("got %d scenes (total %d), want 12", len(scenes), total)
	}
	seen := map[string]bool{}
	for _, sc := range scenes {
		if seen[sc.ID] {
			t.Errorf("scene %s emitted twice", sc.ID)
		}
		seen[sc.ID] = true
	}
	// Page 4 is the empty grid that ends the walk.
	if got := site.count("/listpages/all_4.html"); got != 1 {
		t.Errorf("fetched the past-end page %d times, want 1", got)
	}
}

// The detail page supplies what the card cannot: description, duration, tags
// and categories. Its title and performer win too, being the fuller form.
func TestDetailSuppliesTheRicherFields(t *testing.T) {
	site := catalogueServer(t, 1, 1)
	defer site.Close()

	s := newTestScraper(site.Server)
	scenes, _, _, _ := collect(t, s, "https://www.heyzo.com/", scraper.ListOpts{Workers: 1})
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	got := scenes[0]

	if got.ID != "1000" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Title != "Detail Title 1000" {
		t.Errorf("Title = %q", got.Title)
	}
	// The `memo` row is the site's own synopsis and wins over the JSON-LD
	// description, because it is the one present on every page.
	if got.Description != "The site's own synopsis." {
		t.Errorf("Description = %q", got.Description)
	}
	// PT0H58M54S — and NOT the 9h9m9s on the nested video object.
	if got.Duration != 3534 {
		t.Errorf("Duration = %d, want 3534", got.Duration)
	}
	if got.Date.Format("2006-01-02") != "2024-08-17" {
		t.Errorf("Date = %v", got.Date)
	}
	if len(got.Performers) != 1 || got.Performers[0] != "Jane Eyre" {
		t.Errorf("Performers = %v", got.Performers)
	}
	if strings.Join(got.Categories, ",") != "Mature,Slim" {
		t.Errorf("Categories = %v", got.Categories)
	}
	// The fixture lists 69 twice.
	if strings.Join(got.Tags, ",") != "69,Toys" {
		t.Errorf("Tags = %v", got.Tags)
	}
	// A series rendered as dashes means "none".
	if got.Series != "" {
		t.Errorf("Series = %q, want empty", got.Series)
	}
	if got.Studio != studioName || got.SiteID != siteID {
		t.Errorf("Studio/SiteID = %q/%q", got.Studio, got.SiteID)
	}
	if !strings.HasPrefix(got.URL, site.URL+"/moviepages/") {
		t.Errorf("URL = %q", got.URL)
	}
	if !strings.HasPrefix(got.Thumbnail, site.URL+"/contents/") {
		t.Errorf("Thumbnail = %q", got.Thumbnail)
	}
}

// The newer pages ship no JSON-LD at all. Everything the table holds must
// still be read, and the page must not be reported as unparseable.
func TestPageWithoutJSONLDStillParses(t *testing.T) {
	full := detailHTML("Outer", "Outer Desc", "PT1H0M0S", "2026-01-02", "Hester Prynne",
		[]string{"Mature"}, []string{"Toys"}, "-----")
	i := strings.Index(full, `<script type="application/ld+json">`)
	j := strings.Index(full, "</script>") + len("</script>")
	noLD := full[:i] + full[j:]

	d := parseDetail(noLD)
	if d.empty() {
		t.Fatal("a page with a movieInfo table but no JSON-LD read as unparseable")
	}
	if d.description != "The site's own synopsis." {
		t.Errorf("description = %q", d.description)
	}
	if len(d.performers) != 1 || d.performers[0] != "Hester Prynne" {
		t.Errorf("performers = %v", d.performers)
	}
	if strings.Join(d.tags, ",") != "Toys" {
		t.Errorf("tags = %v", d.tags)
	}
	// Duration only exists in the JSON-LD, so it is legitimately absent here.
	if d.duration != 0 {
		t.Errorf("duration = %d, want 0", d.duration)
	}
}

// The JSON-LD nests the same keys under the Movie and under its video object.
// The outer copy comes first and is the one that must win.
func TestJSONLDPrefersTheOuterMovieFields(t *testing.T) {
	d := parseDetail(detailHTML("Outer Name", "Outer Desc", "PT1H0M0S", "2026-01-02", "Hester Prynne", nil, nil, ""))
	if d.title != "Outer Name" {
		t.Errorf("title = %q", d.title)
	}
	// PT1H0M0S, not the nested video object's PT9H9M9S.
	if d.duration != 3600 {
		t.Errorf("duration = %d, want 3600", d.duration)
	}

	// And the outer description is what the JSON-LD contributes — visible once
	// the memo row, which otherwise wins, is taken away.
	noMemo := strings.Replace(detailHTML("Outer Name", "Outer Desc", "PT1H0M0S", "2026-01-02", "Hester Prynne", nil, nil, ""),
		`<tr class="table-memo"><td colspan="2"><p class="memo">The site's own synopsis.</p></td></tr>`, "", 1)
	if got := parseDetail(noMemo).description; got != "Outer Desc" {
		t.Errorf("description = %q, want the outer JSON-LD value", got)
	}
}

// A card gives the performer twice — as the thumbnail's alt text and as the
// actor link. The link wins; alt is the fallback for a card without one.
func TestCardPerformerFallsBackToTheThumbnailAlt(t *testing.T) {
	withLink := parseCards(card("77", "T", "Hester Prynne", "2026-05-06"))
	if len(withLink) != 1 || withLink[0].performer != "Hester Prynne" {
		t.Fatalf("got %+v", withLink)
	}
	noLink := parseCards(strings.Replace(card("78", "T", "Jane Eyre", "2026-05-06"),
		`<a href="/listpages/actor_293_1.html?sort=pop" class="actor" title="">Jane Eyre</a>`, "", 1))
	if len(noLink) != 1 || noLink[0].performer != "Jane Eyre" {
		t.Fatalf("alt fallback failed: %+v", noLink)
	}
}

// A filtered listing is the same walk over a different stem, and the page
// number in the URL is a starting point, not a restriction.
func TestFilteredListingWalksItsOwnStem(t *testing.T) {
	site := catalogueServer(t, 2, 2)
	defer site.Close()

	s := newTestScraper(site.Server)
	scenes, errs, _, _ := collect(t, s, "https://www.heyzo.com/listpages/actor_293_1.html", scraper.ListOpts{Workers: 1})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4", len(scenes))
	}
	if site.count("/listpages/actor_293_2.html") == 0 {
		t.Error("the filtered walk never reached page 2")
	}
	if site.count("/listpages/all_1.html") != 0 {
		t.Error("a filtered URL also walked the full catalogue")
	}
}

// The listing is newest-first, so a stored id means the rest is stored too.
func TestKnownIDStopsTheWalkEarly(t *testing.T) {
	site := catalogueServer(t, 3, 4)
	defer site.Close()

	s := newTestScraper(site.Server)
	scenes, _, _, stopped := collect(t, s, "https://www.heyzo.com/",
		scraper.ListOpts{Workers: 1, KnownIDs: map[string]bool{"1002": true}})

	if !stopped {
		t.Error("StoppedEarly was not reported")
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want the 2 before the known id", len(scenes))
	}
	if site.count("/listpages/all_2.html") != 0 {
		t.Error("the walk continued past the known id")
	}
}

// An empty first page means the template changed or the filter does not exist,
// which must not read as an empty catalogue to an authoritative --full save.
// An empty page later is just the end.
func TestEmptyFirstPageIsAParseErrorButALaterOneIsNot(t *testing.T) {
	site := catalogueServer(t, 0, 4)
	defer site.Close()

	s := newTestScraper(site.Server)
	scenes, errs, _, _ := collect(t, s, "https://www.heyzo.com/", scraper.ListOpts{Workers: 1})
	if len(scenes) != 0 {
		t.Fatalf("got %d scenes from an empty site", len(scenes))
	}
	if len(errs) == 0 {
		t.Fatal("an empty first page reported no error")
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}

	// With one real page the empty second page ends the walk quietly.
	site2 := catalogueServer(t, 1, 2)
	defer site2.Close()
	s2 := newTestScraper(site2.Server)
	scenes2, errs2, _, _ := collect(t, s2, "https://www.heyzo.com/", scraper.ListOpts{Workers: 1})
	if len(errs2) != 0 {
		t.Errorf("the ordinary end of the walk reported: %v", errs2)
	}
	if len(scenes2) != 2 {
		t.Errorf("got %d scenes, want 2", len(scenes2))
	}
}

// A failed detail costs the description, not the scene — the card already has
// id, title, date, performer and thumbnail. The failure is still reported.
func TestDetailFailureKeepsTheCardOnlyScene(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/moviepages/") {
			http.Error(w, "gone", http.StatusInternalServerError)
			return
		}
		if strings.HasSuffix(r.URL.Path, "all_1.html") {
			_, _ = fmt.Fprint(w, `<html><body>`+card("1000", "Card Title", "Hester Prynne", "2026-08-11")+`</body></html>`)
			return
		}
		_, _ = fmt.Fprint(w, `<html><body></body></html>`)
	}))
	defer srv.Close()

	s := newTestScraper(srv)
	scenes, errs, _, _ := collect(t, s, "https://www.heyzo.com/", scraper.ListOpts{Workers: 1})
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want the card-only scene", len(scenes))
	}
	if scenes[0].Title != "Card Title" || scenes[0].Date.Format("2006-01-02") != "2026-08-11" {
		t.Errorf("card fields lost: %+v", scenes[0])
	}
	if len(errs) == 0 {
		t.Error("the failed detail fetch was not reported")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://heyzo.com",
		"https://www.heyzo.com/",
		"https://en.heyzo.com/moviepages/3400/index.html",
		"http://www.heyzo.com/listpages/all_2.html",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false, want true", u)
		}
	}
	for _, u := range []string{
		"https://heydouga.com/",
		"https://notheyzo.com/",
		"https://example.com/heyzo.com",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true, want false", u)
		}
	}
}

func TestScenesValidate(t *testing.T) {
	site := catalogueServer(t, 1, 3)
	defer site.Close()

	s := newTestScraper(site.Server)
	scenes, _, _, _ := collect(t, s, "https://www.heyzo.com/", scraper.ListOpts{Workers: 2})
	for _, sc := range scenes {
		testutil.ValidateScene(t, sc)
	}
}

func TestContextCancellationStopsTheWalk(t *testing.T) {
	site := catalogueServer(t, 50, 10)
	defer site.Close()

	s := newTestScraper(site.Server)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, "https://www.heyzo.com/", scraper.ListOpts{Workers: 3, Delay: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	cancel()
	for range ch { //nolint:revive // drain so the goroutines can finish their sends
	}
}
