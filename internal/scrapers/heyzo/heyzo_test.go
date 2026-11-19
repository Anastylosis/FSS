package heyzo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// card reproduces one listing tile of the Next.js site: the title sits in an
// `a.title`, the date is a Japanese 2026年8月11日 string, and the thumbnail is
// wrapped in the `/_next/image` optimiser URL.
func card(id, title, performer, date string) string {
	t, _ := time.Parse("2006-01-02", date)
	ja := fmt.Sprintf("%d年%d月%d日", t.Year(), int(t.Month()), t.Day())
	return fmt.Sprintf(`<div class="MuiPaper-root MuiCard-root css-8wfd2x"><a class="MuiButton-root" tabindex="0" href="/movie/%s"><span><img alt="Thumbnail" loading="lazy" srcSet="/_next/image?url=x 1x" src="/_next/image?url=https%%3A%%2F%%2Fwww.heyzo.com%%2Fcontents%%2F3000%%2F%s%%2Fimages%%2Fplayer_thumbnail.jpg&amp;w=1920&amp;q=75"/></span></a><div class="MuiCardContent-root"><a class="title" href="/movie/%s"><div class="MuiTypography-root">%s</div></a><p class="MuiTypography-body2">%s</p><a class="MuiButton-root name css-2y8ajh" tabindex="0" href="/listpages/actor_293_1">%s</a></div></div>`,
		id, id, id, title, ja, performer)
}

// detailHTML reproduces a movie page: the heading, category chips, the
// synopsis paragraph and the labelled item rows. Class names carry the
// CSS-module hash the live site appends.
func detailHTML(title, desc, date, actor string, cats, tags []string, series string) string {
	var chips, tagLinks strings.Builder
	for _, c := range cats {
		fmt.Fprintf(&chips, `<a class="MuiButton-root" tabindex="0" href="/listpages/category_20_1"><p class="MuiTypography-root">%s</p></a>`, c)
	}
	for _, tg := range tags {
		fmt.Fprintf(&tagLinks, `<a href="/listpages/category_7_1">%s</a>`, tg)
	}
	memo := ""
	if desc != "" {
		memo = `<p class="Movie_memo__Oq_R5">` + desc + `</p>`
	}
	seriesRow := ""
	if series != "" {
		seriesRow = `<div class="Movie_item__5i6St"><span>シリーズ</span>` + series + `</div>`
	}
	return fmt.Sprintf(`<html><head><title>%s | HEYZO</title></head><body><main><div><h1>%s</h1></div><div class="MuiBox-root">%s</div>%s<div class="Movie_disclaimer__H6muq">All performers are adults.<br/>Fiction.</div><div class="Movie_item__5i6St"><span>出演</span><span><a href="/listpages/actor_99_1">%s</a></span></div><div class="Movie_item__5i6St"><span>配信日</span>%s</div>%s<div class="Movie_item__5i6St"><span>タグ</span><div class="Movie_tags__5D_UT">%s</div></div><div class="Movie_item__5i6St"><span>評価</span><span>4</span></div></main></body></html>`,
		title, title, chips.String(), memo, actor, date, seriesRow, tagLinks.String())
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
			name := strings.TrimPrefix(r.URL.Path, "/listpages/")
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
		case strings.HasPrefix(r.URL.Path, "/movie/"):
			id := strings.TrimPrefix(r.URL.Path, "/movie/")
			_, _ = fmt.Fprint(w, detailHTML(
				"Detail Title "+id, "A description.", "2024年8月17日", "Jane Eyre",
				[]string{"Mature", "Slim"}, []string{"69", "Toys", "69"}, ""))
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
	if got := site.count("/listpages/all_4"); got != 1 {
		t.Errorf("fetched the past-end page %d times, want 1", got)
	}
}

// The detail page supplies what the card cannot: description, tags and
// categories. Its title and performer win too, being the fuller form.
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
	if got.Description != "A description." {
		t.Errorf("Description = %q", got.Description)
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
	if got.Series != "" {
		t.Errorf("Series = %q, want empty", got.Series)
	}
	if got.Studio != studioName || got.SiteID != siteID {
		t.Errorf("Studio/SiteID = %q/%q", got.Studio, got.SiteID)
	}
	if !strings.HasPrefix(got.URL, site.URL+"/movie/") {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Thumbnail != "https://www.heyzo.com/contents/3000/1000/images/player_thumbnail.jpg" {
		t.Errorf("Thumbnail = %q, want the original asset behind the optimiser URL", got.Thumbnail)
	}
}

// Real pages captured from the live site.
func TestParseLiveListing(t *testing.T) {
	b, err := os.ReadFile("testdata/listing.html")
	if err != nil {
		t.Fatal(err)
	}
	cards := parseCards(string(b))
	if len(cards) != 3 {
		t.Fatalf("got %d cards, want 3", len(cards))
	}
	c := cards[0]
	if c.id != "3983" || c.path != "/movie/3983" || c.title != "ディルドでオナっちゃう女たち～ボーナススペシャルVol.21～" || c.performer != "沢田美波" {
		t.Errorf("card = %+v", c)
	}
	if c.date.Format("2006-01-02") != "2026-11-20" {
		t.Errorf("date = %v", c.date)
	}
	if c.thumb != "https://www.heyzo.com/contents/3000/3983/images/player_thumbnail.jpg" {
		t.Errorf("thumb = %q", c.thumb)
	}
}

func TestParseLiveDetail(t *testing.T) {
	b, err := os.ReadFile("testdata/detail.html")
	if err != nil {
		t.Fatal(err)
	}
	d := parseDetail(string(b))
	if d.empty() || d.title != "浴衣deデート～夏のデートは刺激的に～" {
		t.Fatalf("detail = %+v", d)
	}
	if !strings.HasPrefix(d.description, "夏休みも終わってしまいましたが") || strings.ContainsAny(d.description, "<>") {
		t.Errorf("description = %q", d.description)
	}
	if strings.Join(d.categories, ",") != "AV女優" {
		t.Errorf("categories = %v", d.categories)
	}
	if len(d.performers) != 1 || d.performers[0] != "星崎アンリ" || d.date.Format("2006-01-02") != "2012-08-31" {
		t.Errorf("performers = %v, date = %v", d.performers, d.date)
	}
	if len(d.tags) != 14 || d.tags[0] != "中出し" || d.tags[13] != "ローター" {
		t.Errorf("tags = %v", d.tags)
	}
}

// Newer bonus movies publish no synopsis; that must not read as a parse
// failure, and the chips under the heading are still categories.
func TestDetailWithoutSynopsisStillParses(t *testing.T) {
	b, err := os.ReadFile("testdata/detail_bonus.html")
	if err != nil {
		t.Fatal(err)
	}
	d := parseDetail(string(b))
	if d.empty() || d.description != "" {
		t.Fatalf("detail = %+v", d)
	}
	if strings.Join(d.categories, ",") != "素人,ボーナス動画" {
		t.Errorf("categories = %v", d.categories)
	}
	if len(d.performers) != 1 || d.performers[0] != "根岸由香" {
		t.Errorf("performers = %v", d.performers)
	}
}

func TestDetailWithoutHeadingIsEmpty(t *testing.T) {
	if !parseDetail(`<html><body><p>Not Found</p></body></html>`).empty() {
		t.Error("a page with no movie heading read as parsed")
	}
}

// A filtered listing is the same walk over a different stem, and the page
// number in the URL is a starting point, not a restriction.
func TestFilteredListingWalksItsOwnStem(t *testing.T) {
	site := catalogueServer(t, 2, 2)
	defer site.Close()

	s := newTestScraper(site.Server)
	scenes, errs, _, _ := collect(t, s, "https://www.heyzo.com/listpages/actor_293_1", scraper.ListOpts{Workers: 1})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4", len(scenes))
	}
	if site.count("/listpages/actor_293_2") == 0 {
		t.Error("the filtered walk never reached page 2")
	}
	if site.count("/listpages/all_1") != 0 {
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
	if site.count("/listpages/all_2") != 0 {
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
		if strings.HasPrefix(r.URL.Path, "/movie/") {
			http.Error(w, "gone", http.StatusInternalServerError)
			return
		}
		if strings.HasSuffix(r.URL.Path, "all_1") {
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
		"https://en.heyzo.com/movie/3400",
		"http://www.heyzo.com/listpages/all_2",
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
