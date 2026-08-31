package modelcentroutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestSlugify(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"Invaded and Degraded", "invaded-and-degraded"},
		{"Hot MILF POV!", "hot-milf-pov"},
		{"  Leading Spaces  ", "leading-spaces"},
		{"Multiple---Dashes", "multiple-dashes"},
		{"Title With (Parens) & Symbols", "title-with-parens-symbols"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Slugify(c.input); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestParsePublishDate(t *testing.T) {
	sc := APIScene{ID: 100}
	sc.Sites.Collection = map[string]APISiteEntry{
		"100": {PublishDate: "2026-04-24 10:00:00"},
	}
	d := ParsePublishDate(sc)
	if d.Year() != 2026 || d.Month() != 4 || d.Day() != 24 {
		t.Errorf("date = %v", d)
	}
}

func TestParsePublishDateFallback(t *testing.T) {
	sc := APIScene{ID: 100}
	sc.Sites.Collection = map[string]APISiteEntry{
		"999": {PublishDate: "2025-01-15 08:00:00"},
	}
	d := ParsePublishDate(sc)
	if d.Year() != 2025 || d.Month() != 1 || d.Day() != 15 {
		t.Errorf("date = %v", d)
	}
}

func TestParseTagsWrapped(t *testing.T) {
	raw := json.RawMessage(`{"collection":{"10":{"alias":"milf"},"20":{"alias":"pov"}}}`)
	tags := ParseTags(raw)
	if len(tags) != 2 {
		t.Fatalf("got %d tags, want 2", len(tags))
	}
	found := map[string]bool{}
	for _, tag := range tags {
		found[tag] = true
	}
	if !found["milf"] || !found["pov"] {
		t.Errorf("tags = %v", tags)
	}
}

func TestParseTagsFlat(t *testing.T) {
	raw := json.RawMessage(`{"10":{"alias":"blonde"},"20":{"alias":"dirty talk"}}`)
	tags := ParseTags(raw)
	if len(tags) != 2 {
		t.Fatalf("got %d tags, want 2", len(tags))
	}
	found := map[string]bool{}
	for _, tag := range tags {
		found[tag] = true
	}
	if !found["blonde"] || !found["dirty talk"] {
		t.Errorf("tags = %v", tags)
	}
}

func TestParseTagsEmpty(t *testing.T) {
	if tags := ParseTags(nil); tags != nil {
		t.Errorf("nil input: got %v", tags)
	}
	if tags := ParseTags(json.RawMessage(`{}`)); len(tags) != 0 {
		t.Errorf("empty object: got %v", tags)
	}
}

func TestToScene(t *testing.T) {
	cfg := SiteConfig{
		SiteID:     "testsite",
		SiteBase:   "https://testsite.com",
		StudioName: "Test Site",
		Performers: []string{"Performer One"},
	}

	listing := APIScene{
		ID:    12345,
		Title: "Test Scene Title",
		Len:   600,
	}
	listing.Sites.Collection = map[string]APISiteEntry{
		"12345": {PublishDate: "2026-03-15 12:00:00"},
	}

	detail := &APIScene{
		Description: "A great description",
		Tags:        json.RawMessage(`{"collection":{"1":{"alias":"tag1"},"2":{"alias":"tag2"}}}`),
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sc := ToScene(cfg, listing, detail, "https://testsite.com/videos", now)

	if sc.ID != "12345" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != "testsite" {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.URL != "https://testsite.com/scene/12345/test-scene-title" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Title != "Test Scene Title" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Duration != 600 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if sc.Date.Month() != 3 || sc.Date.Day() != 15 {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.Studio != "Test Site" {
		t.Errorf("Studio = %q", sc.Studio)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Performer One" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Description != "A great description" {
		t.Errorf("Description = %q", sc.Description)
	}
	if len(sc.Tags) != 2 {
		t.Errorf("Tags = %v", sc.Tags)
	}
}

func TestToSceneNoPerformers(t *testing.T) {
	cfg := SiteConfig{
		SiteID:     "testsite",
		SiteBase:   "https://testsite.com",
		StudioName: "Test Site",
	}

	listing := APIScene{ID: 1, Title: "Scene", Len: 300}
	listing.Sites.Collection = map[string]APISiteEntry{}

	now := time.Now().UTC()
	sc := ToScene(cfg, listing, nil, "https://testsite.com/videos", now)

	if sc.Performers != nil {
		t.Errorf("Performers = %v, want nil", sc.Performers)
	}
}

func TestDomainFromBase(t *testing.T) {
	cases := []struct {
		base, want string
	}{
		{"https://example.com", "example.com"},
		{"https://www.example.com/", "www.example.com"},
		{"http://test.net", "test.net"},
	}
	for _, c := range cases {
		if got := domainFromBase(c.base); got != c.want {
			t.Errorf("domainFromBase(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}

type testScene struct {
	id       int
	title    string
	length   int
	date     string
	desc     string
	tagsJSON string
}

func listingResponse(scenes []testScene, total int) []byte {
	collection := make([]map[string]any, len(scenes))
	for i, sc := range scenes {
		collection[i] = map[string]any{
			"id":     sc.id,
			"title":  sc.title,
			"length": sc.length,
			"sites": map[string]any{
				"collection": map[string]any{
					strconv.Itoa(sc.id): map[string]string{
						"publishDate": sc.date,
					},
				},
			},
		}
	}
	resp := map[string]any{
		"status": true,
		"response": map[string]any{
			"collection": collection,
			"meta":       map[string]int{"totalCount": total},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func detailResponse(sc testScene) []byte {
	tags := json.RawMessage(`{}`)
	if sc.tagsJSON != "" {
		tags = json.RawMessage(sc.tagsJSON)
	}
	collection := []map[string]any{{
		"id":          sc.id,
		"title":       sc.title,
		"length":      sc.length,
		"description": sc.desc,
		"tags":        tags,
		"sites": map[string]any{
			"collection": map[string]any{
				strconv.Itoa(sc.id): map[string]string{
					"publishDate": sc.date,
				},
			},
		},
	}}
	resp := map[string]any{
		"status": true,
		"response": map[string]any{
			"collection": collection,
			"meta":       map[string]int{"totalCount": 1},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func newTestServer(scenes [][]testScene, total int, details map[int]testScene) *httptest.Server {
	pageIdx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()

		if filterID := q.Get("filter[id][values][0]"); filterID != "" {
			id, _ := strconv.Atoi(filterID)
			if sc, ok := details[id]; ok {
				_, _ = w.Write(detailResponse(sc))
				return
			}
			_, _ = fmt.Fprint(w, `{"status":true,"response":{"collection":[],"meta":{"totalCount":0}}}`)
			return
		}

		if pageIdx >= len(scenes) {
			_, _ = fmt.Fprint(w, `{"status":true,"response":{"collection":[],"meta":{"totalCount":0}}}`)
			return
		}
		page := scenes[pageIdx]
		pageIdx++
		_, _ = w.Write(listingResponse(page, total))
	}))
}

func TestListScenes(t *testing.T) {
	scenes := []testScene{
		{id: 100, title: "Scene One", length: 600, date: "2026-04-20 10:00:00", desc: "Desc one", tagsJSON: `{"collection":{"1":{"alias":"tag1"}}}`},
		{id: 200, title: "Scene Two", length: 900, date: "2026-04-15 10:00:00", desc: "Desc two", tagsJSON: `{"collection":{"2":{"alias":"tag2"}}}`},
	}
	details := map[int]testScene{100: scenes[0], 200: scenes[1]}

	ts := newTestServer([][]testScene{scenes}, 2, details)
	defer ts.Close()

	s := &Scraper{
		cfg:    SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test"},
		Client: ts.Client(),
	}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
	if results[0].Title != "Scene One" {
		t.Errorf("first title = %q", results[0].Title)
	}
	if results[0].Description != "Desc one" {
		t.Errorf("first desc = %q", results[0].Description)
	}
	if results[0].Duration != 600 {
		t.Errorf("first duration = %d", results[0].Duration)
	}
}

func TestListScenesPagination(t *testing.T) {
	page1 := make([]testScene, PerPage)
	for i := range page1 {
		page1[i] = testScene{
			id: i + 1, title: fmt.Sprintf("Scene %d", i+1),
			length: 300, date: "2026-01-01 00:00:00",
		}
	}
	page2 := []testScene{
		{id: 101, title: "Scene 101", length: 300, date: "2026-01-01 00:00:00"},
		{id: 102, title: "Scene 102", length: 300, date: "2026-01-01 00:00:00"},
	}

	allDetails := map[int]testScene{}
	for _, sc := range page1 {
		allDetails[sc.id] = sc
	}
	for _, sc := range page2 {
		allDetails[sc.id] = sc
	}

	ts := newTestServer([][]testScene{page1, page2}, 102, allDetails)
	defer ts.Close()

	s := &Scraper{
		cfg:    SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test"},
		Client: ts.Client(),
	}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 102 {
		t.Fatalf("got %d scenes, want 102", len(results))
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	scenes := []testScene{
		{id: 1, title: "New", length: 300, date: "2026-04-20 10:00:00"},
		{id: 2, title: "Also New", length: 300, date: "2026-04-19 10:00:00"},
		{id: 3, title: "Known", length: 300, date: "2026-04-18 10:00:00"},
		{id: 4, title: "Old", length: 300, date: "2026-04-17 10:00:00"},
	}
	details := map[int]testScene{1: scenes[0], 2: scenes[1]}

	ts := newTestServer([][]testScene{scenes}, 4, details)
	defer ts.Close()

	s := &Scraper{
		cfg:    SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test"},
		Client: ts.Client(),
	}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{
		KnownIDs: map[string]bool{"3": true},
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
	if results[0].ID != "1" || results[1].ID != "2" {
		t.Errorf("scenes = %v, %v", results[0].ID, results[1].ID)
	}
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build APIScene values in Go, so encode and decode share the
// struct tag and a renamed one round-trips unnoticed. This is a live
// /api/content.load response from bigjohnnyxxx.com, kept whole and unedited
// (980 bytes). No credential — the endpoint is open, though the field list must
// be requested explicitly via the `fields[N]` query parameters.
//
// Shapes a hand-written fixture would have got wrong:
//   - **`sites.collection` is a map keyed by the scene ID as a *string***
//     (`{"7720208": {"publishDate": …}}`), not an array. That is why
//     APIScene.Sites.Collection is a `map[string]APISiteEntry` and why
//     ParsePublishDate does `strconv.Itoa(sc.ID)` to look itself up. Writing it
//     as an array — the obvious guess — decodes to an empty map and every scene
//     gets a zero Date.
//   - `publishDate` is space-separated with no zone ("2019-12-08 23:00:00").
//   - the payload is double-wrapped: `{"status":…,"response":{"collection":…}}`,
//     with the count under `response.meta.totalCount` rather than at the top.
//   - the response also carries a PHP `class` marker
//     ("Adultcentro\\Amc\\Object\\Content_Site") that nothing decodes — retained,
//     so unknown-field tolerance stays covered.
func TestGoldenContentLoad(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "content_load.json"))
	if err != nil {
		t.Fatal(err)
	}

	var resp APIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if !resp.Status {
		t.Fatal("status is false (status)")
	}
	if len(resp.Response.Collection) != 2 {
		t.Fatalf("decoded %d scenes, want 2", len(resp.Response.Collection))
	}
	if resp.Response.Meta.TotalCount != 65 {
		t.Errorf("TotalCount = %d (response.meta.totalCount), want 65 — paging depends on it",
			resp.Response.Meta.TotalCount)
	}

	sc := resp.Response.Collection[0]
	if sc.ID != 7720208 {
		t.Errorf("ID = %d (id), want 7720208", sc.ID)
	}
	if sc.Title == "" {
		t.Error("Title is empty (title)")
	}
	if sc.Len == 0 {
		t.Error("Len is 0 (length) — note the field is `length`, not `duration`")
	}

	// The id-keyed map, which is the whole point of this fixture.
	if len(sc.Sites.Collection) == 0 {
		t.Fatal("Sites.Collection is empty (sites.collection) — it is a map keyed by the " +
			"scene ID as a string, not an array; an array guess decodes to nothing")
	}
	entry, ok := sc.Sites.Collection[strconv.Itoa(sc.ID)]
	if !ok {
		t.Fatalf("sites.collection has no key %q; keys are %v", strconv.Itoa(sc.ID), sc.Sites.Collection)
	}
	if entry.PublishDate != "2019-12-08 23:00:00" {
		t.Errorf("publishDate = %q, want a space-separated timestamp with no zone", entry.PublishDate)
	}

	// And that ParsePublishDate still resolves it.
	if d := ParsePublishDate(sc); d.IsZero() {
		t.Error("ParsePublishDate returned zero on a real payload — Scene.Date comes from it")
	}
}

func TestGoldenContentLoadKeepsIDKeyedMap(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "content_load.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"collection":{"7720208":`)) {
		t.Error(`fixture lost the id-keyed sites.collection map; if the API switched to an ` +
			`array, APIScene.Sites.Collection must change type too`)
	}
	if !bytes.Contains(body, []byte(`"class":`)) {
		t.Error("fixture lost the PHP class marker; unknown-field tolerance is no longer covered")
	}
}

// A detail call that fails costs the description and tags, not the rest of the
// catalogue. Aborting the walk there handed --full's authoritative Save a
// truncated studio.
func TestDetailFailureDoesNotAbortTheWalk(t *testing.T) {
	const items = 3
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("filter[id][values][0]") != "" || strings.Contains(q.Get("transitParameters[preset]"), "scene") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var sb strings.Builder
		sb.WriteString(`{"status":true,"response":{"collection":[`)
		for i := 0; i < items; i++ {
			if i > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `{"id":%d,"title":"Scene %d","length":600}`, 100+i, i)
		}
		fmt.Fprintf(&sb, `],"meta":{"totalCount":%d}}}`, items)
		_, _ = fmt.Fprint(w, sb.String())
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test"})
	s.Client = ts.Client()

	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	scenes, errs := 0, 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes++
			if res.Scene.Title == "" {
				t.Error("a scene without its detail still keeps the listing fields")
			}
		case scraper.KindError:
			errs++
		}
	}
	if scenes != items {
		t.Errorf("scenes = %d, want %d", scenes, items)
	}
	if errs != items {
		t.Errorf("errors = %d, want %d", errs, items)
	}
}
