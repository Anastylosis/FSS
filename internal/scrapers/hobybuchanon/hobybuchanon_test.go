package hobybuchanon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// item mirrors the fields the live `__NEXT_DATA__` payload carries for one
// scene. Values are shaped like the real ones (dates "2006/01/02 15:04:05",
// HTML entities in the description) but the catalogue is invented.
func item(id int, title, slug, site string) map[string]any {
	return map[string]any{
		"id":               id,
		"title":            title,
		"slug":             slug,
		"site":             site,
		"publish_date":     "2026/03/1" + strconv.Itoa(id%10) + " 12:00:00",
		"seconds_duration": 1800 + id,
		"description":      "A &amp; B <strong>rough</strong>  scene.",
		"thumb":            "https://cdn.example/" + slug + ".jpg",
		"trailer_url":      "https://cdn.example/" + slug + ".mp4",
		"tags":             []string{},
		"models":           []string{"Hester Prynne"},
		"models_slugs":     []map[string]string{{"name": "Hester Prynne", "slug": "hester-prynne"}},
		"views":            100 + id,
	}
}

func nextData(t *testing.T, pageProps map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"props": map[string]any{"pageProps": pageProps}})
	if err != nil {
		t.Fatalf("marshalling fixture: %v", err)
	}
	return `<!doctype html><html><body><script id="__NEXT_DATA__" type="application/json">` +
		string(b) + `</script></body></html>`
}

// listingServer serves /updates with `total` items paginated at perPage, in the
// same shape the live tour uses.
func listingServer(t *testing.T, total, perPage int) *httptest.Server {
	t.Helper()
	totalPages := (total + perPage - 1) / perPage
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/updates" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		var data []map[string]any
		for i := (page - 1) * perPage; i < total && i < page*perPage; i++ {
			site := "hobybuchanon"
			if i%5 == 0 {
				site = "Suck This Dick"
			}
			data = append(data, item(i+1, fmt.Sprintf("Scene %d", i+1), fmt.Sprintf("scene-%d", i+1), site))
		}
		_, _ = fmt.Fprint(w, nextData(t, map[string]any{
			"contents": map[string]any{
				"total": total, "page": page, "per_page": perPage,
				"total_pages": totalPages, "data": data,
			},
		}))
	}))
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, []error, int) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []models.Scene
	var errs []error
	total := 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindError:
			errs = append(errs, res.Err)
		case scraper.KindTotal:
			total = res.Total
		}
	}
	return scenes, errs, total
}

func TestListScenesWalksEveryPage(t *testing.T) {
	srv := listingServer(t, 19, 8)
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.baseOverride = srv.URL

	scenes, errs, total := collect(t, s, "https://hobybuchanon.com/", scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 19 {
		t.Fatalf("got %d scenes, want 19", len(scenes))
	}
	if total != 19 {
		t.Errorf("progress total = %d, want 19", total)
	}

	seen := map[string]bool{}
	for _, sc := range scenes {
		if seen[sc.ID] {
			t.Errorf("scene %s emitted twice", sc.ID)
		}
		seen[sc.ID] = true
	}
}

func TestSceneFieldsComeFromThePayload(t *testing.T) {
	srv := listingServer(t, 8, 8)
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.baseOverride = srv.URL

	scenes, _, _ := collect(t, s, "https://hobybuchanon.com/", scraper.ListOpts{})
	if len(scenes) == 0 {
		t.Fatal("no scenes")
	}
	got := scenes[0]

	if got.ID != "1" || got.SiteID != siteID {
		t.Errorf("ID/SiteID = %q/%q", got.ID, got.SiteID)
	}
	if got.Title != "Scene 1" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Studio != studioName {
		t.Errorf("Studio = %q, want %q", got.Studio, studioName)
	}
	if want := srv.URL + "/updates/scene-1"; got.URL != want {
		t.Errorf("URL = %q, want %q", got.URL, want)
	}
	// Description is HTML in the payload: entities decoded, tags dropped,
	// runs of whitespace collapsed.
	if want := "A & B rough scene."; got.Description != want {
		t.Errorf("Description = %q, want %q", got.Description, want)
	}
	if got.Duration != 1801 {
		t.Errorf("Duration = %d, want 1801", got.Duration)
	}
	if got.Date.Format("2006-01-02") != "2026-03-11" {
		t.Errorf("Date = %v", got.Date)
	}
	if len(got.Performers) != 1 || got.Performers[0] != "Hester Prynne" {
		t.Errorf("Performers = %v", got.Performers)
	}
	if got.Thumbnail == "" || got.Preview == "" {
		t.Errorf("Thumbnail/Preview = %q/%q", got.Thumbnail, got.Preview)
	}
	if got.ScrapedAt.IsZero() {
		t.Error("ScrapedAt not stamped")
	}
	// Nothing on this tour is priced, so no snapshot should be recorded.
	if len(got.PriceHistory) != 0 {
		t.Errorf("PriceHistory = %v, want none", got.PriceHistory)
	}
}

// The payload's `site` field is the sub-brand a scene was published under. It
// becomes Series only when it differs from the hub, so the bulk of the
// catalogue carries no Series echoing the studio name.
func TestSubBrandBecomesSeriesAndTheHubDoesNot(t *testing.T) {
	srv := listingServer(t, 8, 8)
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.baseOverride = srv.URL

	scenes, _, _ := collect(t, s, "https://hobybuchanon.com/", scraper.ListOpts{})

	var hub, sub int
	for _, sc := range scenes {
		switch sc.Series {
		case "":
			hub++
		case "Suck This Dick":
			sub++
		default:
			t.Errorf("scene %s has unexpected Series %q", sc.ID, sc.Series)
		}
	}
	if sub == 0 {
		t.Error("no scene picked up the sub-brand as Series")
	}
	if hub == 0 {
		t.Error("every scene got a Series; hub scenes should have none")
	}
}

// A model page carries the whole filmography in `model_contents` with no
// pagination, so the model mode reads one page and stops.
func TestModelPageReadsModelContents(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/hobyshotties/") {
			t.Errorf("unexpected request for %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		hits++
		_, _ = fmt.Fprint(w, nextData(t, map[string]any{
			"model_contents": []map[string]any{
				item(7, "Her First", "her-first", "hobybuchanon"),
				item(9, "Her Second", "her-second", "Suck This Dick"),
			},
		}))
	}))
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.baseOverride = srv.URL

	scenes, errs, total := collect(t, s, "https://hobybuchanon.com/hobyshotties/hester-prynne", scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if hits != 1 {
		t.Errorf("fetched the model page %d times, want 1", hits)
	}
	if len(scenes) != 2 || total != 2 {
		t.Fatalf("got %d scenes (total %d), want 2", len(scenes), total)
	}
	if scenes[0].ID != "7" || scenes[1].ID != "9" {
		t.Errorf("ids = %s, %s", scenes[0].ID, scenes[1].ID)
	}
}

// The bare model index lists models, not scenes, so it must fall through to
// the full listing rather than be read as a model page.
func TestModelIndexFallsThroughToTheListing(t *testing.T) {
	srv := listingServer(t, 3, 8)
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.baseOverride = srv.URL

	scenes, errs, _ := collect(t, s, "https://hobybuchanon.com/hobyshotties", scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3 from the listing", len(scenes))
	}
}

// A page that loads but carries no hydration blob is a template change, not an
// empty catalogue, so it must be reported as a parse failure rather than
// returned as a silent success.
func TestMissingHydrationBlobIsAParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<html><body>nothing here</body></html>")
	}))
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.baseOverride = srv.URL

	scenes, errs, _ := collect(t, s, "https://hobybuchanon.com/", scraper.ListOpts{})
	if len(scenes) != 0 {
		t.Fatalf("got %d scenes from a blank page", len(scenes))
	}
	if len(errs) == 0 {
		t.Fatal("a page with no __NEXT_DATA__ reported no error")
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://hobybuchanon.com",
		"https://hobybuchanon.com/",
		"https://www.hobybuchanon.com/updates",
		"http://hobybuchanon.com/hobyshotties/hester-prynne",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false, want true", u)
		}
	}
	for _, u := range []string{
		"https://example.com/hobybuchanon.com",
		"https://hobybuchanonfan.com/",
		"https://suckthisdick.com/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true, want false", u)
		}
	}
}

func TestScenesValidate(t *testing.T) {
	srv := listingServer(t, 4, 8)
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.baseOverride = srv.URL

	scenes, _, _ := collect(t, s, "https://hobybuchanon.com/", scraper.ListOpts{})
	for _, sc := range scenes {
		testutil.ValidateScene(t, sc)
	}
}

func TestContextCancellationStopsTheWalk(t *testing.T) {
	srv := listingServer(t, 200, 8)
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.baseOverride = srv.URL

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, "https://hobybuchanon.com/", scraper.ListOpts{Delay: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	cancel()
	for range ch { //nolint:revive // drain so the goroutine can finish its sends
	}
}

// The live tour returns the pagination fields as JSON numbers on `/updates`
// and as quoted strings on `/updates?page=N`. A strict int decode fails the
// whole listing from page one, so both forms must parse.
func TestQuotedPaginationFieldsParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" {
			page = "1"
		}
		// Quoted numbers throughout, exactly as the tour emits them once a
		// page parameter is present.
		_, _ = fmt.Fprintf(w, `<script id="__NEXT_DATA__" type="application/json">
{"props":{"pageProps":{"contents":{"total":"2","page":%q,"per_page":"8","total_pages":"1",
"data":[{"id":"41","title":"One","slug":"one","site":"hobybuchanon","seconds_duration":"600",
"publish_date":"2026/01/02 00:00:00","views":"7","models_slugs":[{"name":"Hester Prynne","slug":"hester-prynne"}]},
{"id":"42","title":"Two","slug":"two","site":"hobybuchanon","seconds_duration":"700",
"publish_date":"2026/01/03 00:00:00","views":"8","models_slugs":[{"name":"Hester Prynne","slug":"hester-prynne"}]}]}}}}
</script>`, page)
	}))
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.baseOverride = srv.URL

	scenes, errs, total := collect(t, s, "https://hobybuchanon.com/", scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 2 || total != 2 {
		t.Fatalf("got %d scenes (total %d), want 2", len(scenes), total)
	}
	if scenes[0].ID != "41" || scenes[0].Duration != 600 || scenes[0].Views != 7 {
		t.Errorf("quoted numerics decoded wrong: %+v", scenes[0])
	}
}

// An unparseable numeric zeroes that one field rather than failing the page,
// so a single odd value cannot cost a whole listing its scenes.
func TestUnparseableNumericZeroesOnlyThatField(t *testing.T) {
	var f flexInt
	if err := f.UnmarshalJSON([]byte(`"not a number"`)); err != nil {
		t.Fatalf("UnmarshalJSON returned %v, want nil", err)
	}
	if f != 0 {
		t.Errorf("f = %d, want 0", f)
	}
}
