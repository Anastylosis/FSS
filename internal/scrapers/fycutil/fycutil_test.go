package fycutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

var _ scraper.StudioScraper = (*Scraper)(nil)

// buildNuxtData builds a minimal Nuxt 3 devalue-format flat array for testing.
// The structure mirrors what FYC sites produce:
//
//	[0] root object → {data: →1}
//	[1] data object → {tourMainPageData: →2}
//	[2] tourMainPageData → {latestReleases: →3}
//	[3] latestReleases → {items: →4, pagination: →5}
//	[4] items array → [→6]
//	[5] pagination → {nextPage: →7 or nil}
//	[6] release object (indices into further slots)
//	[7+] leaf values
func buildNuxtData(releases []map[string]any, hasNext bool) string {
	// Reserve index 0 for the root object — it must be at position 0
	// in the devalue flat array. We fill it in at the end.
	flat := make([]any, 1, 64)
	push := func(v any) int { i := len(flat); flat = append(flat, v); return i }

	type releaseRef struct {
		obj map[string]any
	}
	var releaseRefs []releaseRef

	for _, rel := range releases {
		obj := make(map[string]any)
		for k, v := range rel {
			switch val := v.(type) {
			case []any:
				elemRefs := make([]any, len(val))
				for i, elem := range val {
					elemRefs[i] = float64(push(elem))
				}
				arrIdx := push(elemRefs)
				obj[k] = float64(arrIdx)
			case map[string]any:
				inner := make(map[string]any)
				for ik, iv := range val {
					inner[ik] = float64(push(iv))
				}
				obj[k] = float64(push(inner))
			default:
				obj[k] = float64(push(v))
			}
		}
		releaseRefs = append(releaseRefs, releaseRef{obj: obj})
	}

	itemIndices := make([]any, len(releaseRefs))
	for i, rr := range releaseRefs {
		itemIndices[i] = float64(push(rr.obj))
	}
	itemsIdx := push(itemIndices)

	var nextPageVal any
	if hasNext {
		nextPageVal = float64(push("/next"))
	}
	pagIdx := push(map[string]any{"nextPage": nextPageVal})

	sectionIdx := push(map[string]any{
		"items":      float64(itemsIdx),
		"pagination": float64(pagIdx),
	})

	tourIdx := push(map[string]any{"latestReleases": float64(sectionIdx)})
	dataIdx := push(map[string]any{"tourMainPageData": float64(tourIdx)})

	flat[0] = map[string]any{"data": float64(dataIdx)}

	b, _ := json.Marshal(flat)
	return string(b)
}

func wrapHTML(nuxtJSON string) string {
	return fmt.Sprintf(`<html><head></head><body><script type="application/json" id="__NUXT_DATA__">%s</script></body></html>`, nuxtJSON)
}

func TestResolverBasic(t *testing.T) {
	// Simple devalue array: [{"key": 1}, "hello"]
	// Index 0 is an object with key→1, index 1 is "hello".
	flat := []any{
		map[string]any{"key": float64(1)},
		"hello",
	}
	r := &resolver{data: flat, memo: make(map[int]any, len(flat))}
	result := r.resolve(0)
	obj, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if obj["key"] != "hello" {
		t.Errorf("key = %v, want hello", obj["key"])
	}
}

func TestResolverNestedObject(t *testing.T) {
	// [{"inner": 1}, {"name": 2}, "Alice"]
	flat := []any{
		map[string]any{"inner": float64(1)},
		map[string]any{"name": float64(2)},
		"Alice",
	}
	r := &resolver{data: flat, memo: make(map[int]any, len(flat))}
	result := r.resolve(0)
	obj := result.(map[string]any)
	inner := obj["inner"].(map[string]any)
	if inner["name"] != "Alice" {
		t.Errorf("inner.name = %v, want Alice", inner["name"])
	}
}

func TestResolverArray(t *testing.T) {
	// [{"items": 1}, [2, 3], "a", "b"]
	flat := []any{
		map[string]any{"items": float64(1)},
		[]any{float64(2), float64(3)},
		"a",
		"b",
	}
	r := &resolver{data: flat, memo: make(map[int]any, len(flat))}
	result := r.resolve(0)
	obj := result.(map[string]any)
	items := obj["items"].([]any)
	if len(items) != 2 || items[0] != "a" || items[1] != "b" {
		t.Errorf("items = %v, want [a b]", items)
	}
}

func TestResolverShallowReactive(t *testing.T) {
	// ["ShallowReactive", 1] at index 0 should redirect to index 1.
	flat := []any{
		[]any{"ShallowReactive", float64(1)},
		map[string]any{"val": float64(2)},
		"resolved",
	}
	r := &resolver{data: flat, memo: make(map[int]any, len(flat))}
	result := r.resolve(0)
	obj := result.(map[string]any)
	if obj["val"] != "resolved" {
		t.Errorf("val = %v, want resolved", obj["val"])
	}
}

func TestWalkPath(t *testing.T) {
	// Build a nested structure and walk into it.
	flat := []any{
		map[string]any{"data": float64(1)},                       // 0: root
		map[string]any{"tourMainPageData": float64(2)},           // 1: data
		map[string]any{"latestReleases": float64(3)},             // 2: tourMainPageData
		map[string]any{"items": float64(4), "count": float64(5)}, // 3: latestReleases
		[]any{},     // 4: items (empty)
		float64(42), // 5: count
	}
	r := &resolver{data: flat, memo: make(map[int]any, len(flat))}

	result, ok := r.walkPath(0, "data", "tourMainPageData", "latestReleases")
	if !ok {
		t.Fatal("walkPath returned false")
	}
	obj := result.(map[string]any)
	if obj["count"] != float64(42) {
		t.Errorf("count = %v, want 42", obj["count"])
	}
}

func TestWalkPathMissing(t *testing.T) {
	flat := []any{
		map[string]any{"data": float64(1)},
		map[string]any{"other": float64(2)},
		"val",
	}
	r := &resolver{data: flat, memo: make(map[int]any, len(flat))}
	_, ok := r.walkPath(0, "data", "tourMainPageData")
	if ok {
		t.Error("walkPath should return false for missing key")
	}
}

func TestFetchPageParsesReleases(t *testing.T) {
	nuxt := buildNuxtData([]map[string]any{
		{
			"id":          float64(123),
			"cachedSlug":  "test-scene",
			"title":       "Test Scene Title",
			"description": "A test description.",
			"releasedAt":  "2025-03-15T00:00:00.000Z",
			"thumbUrl":    "https://cdn.example.com/thumb.jpg",
			"posterUrl":   "https://cdn.example.com/poster.jpg",
			"actors": []any{
				map[string]any{"name": "Jane Doe"},
				map[string]any{"name": "John Smith"},
			},
			"tags": []any{"tag1", "tag2"},
		},
	}, false)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, wrapHTML(nuxt))
	}))
	defer ts.Close()

	cfg := SiteConfig{SiteID: "test", Domain: strings.TrimPrefix(ts.URL, "http://"), StudioName: "Test"}
	s := New(cfg)

	releases, hasNext, err := s.fetchPage(context.Background(), ts.URL, "tourMainPageData", "latestReleases")
	if err != nil {
		t.Fatalf("fetchPage: %v", err)
	}
	if hasNext {
		t.Error("expected hasNext=false")
	}
	if len(releases) != 1 {
		t.Fatalf("got %d releases, want 1", len(releases))
	}

	rel := releases[0]
	if rel["title"] != "Test Scene Title" {
		t.Errorf("title = %v", rel["title"])
	}
	actors, ok := rel["actors"].([]any)
	if !ok || len(actors) != 2 {
		t.Errorf("actors = %v", rel["actors"])
	}
}

func TestFetchPageHasNext(t *testing.T) {
	nuxt := buildNuxtData([]map[string]any{
		{"id": float64(1), "cachedSlug": "s", "title": "S"},
	}, true)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, wrapHTML(nuxt))
	}))
	defer ts.Close()

	cfg := SiteConfig{SiteID: "test", Domain: strings.TrimPrefix(ts.URL, "http://"), StudioName: "Test"}
	s := New(cfg)

	_, hasNext, err := s.fetchPage(context.Background(), ts.URL, "tourMainPageData", "latestReleases")
	if err != nil {
		t.Fatalf("fetchPage: %v", err)
	}
	if !hasNext {
		t.Error("expected hasNext=true")
	}
}

func TestToScene(t *testing.T) {
	cfg := SiteConfig{SiteID: "passionhd", Domain: "passion-hd.com", StudioName: "Passion HD"}
	s := New(cfg)

	rel := map[string]any{
		"id":          float64(456),
		"cachedSlug":  "romantic-evening",
		"title":       "Romantic Evening",
		"description": "  A romantic scene.  ",
		"releasedAt":  "2025-06-01T00:00:00.000Z",
		"thumbUrl":    "https://cdn.example.com/thumb.jpg",
		"posterUrl":   "https://cdn.example.com/poster.jpg",
		"actors":      []any{map[string]any{"name": "Alice"}},
		"tags":        []any{"romantic", "hd"},
	}

	scene := s.toScene(rel, "https://passion-hd.com")
	if scene.ID != "456" {
		t.Errorf("ID = %q, want 456", scene.ID)
	}
	if scene.SiteID != "passionhd" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Title != "Romantic Evening" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.URL != "https://passion-hd.com/video/romantic-evening" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Date.Format("2006-01-02") != "2025-06-01" {
		t.Errorf("Date = %v", scene.Date)
	}
	if scene.Description != "A romantic scene." {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Thumbnail != "https://cdn.example.com/poster.jpg" {
		t.Errorf("Thumbnail = %q (poster should win over thumb)", scene.Thumbnail)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Alice" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if len(scene.Tags) != 2 {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Studio != "Passion HD" {
		t.Errorf("Studio = %q", scene.Studio)
	}
}

func TestToScenePosterFallback(t *testing.T) {
	cfg := SiteConfig{SiteID: "test", Domain: "test.com", StudioName: "Test"}
	s := New(cfg)

	rel := map[string]any{
		"id":         float64(1),
		"cachedSlug": "x",
		"title":      "X",
		"thumbUrl":   "https://cdn.example.com/thumb.jpg",
	}

	scene := s.toScene(rel, "https://test.com")
	if scene.Thumbnail != "https://cdn.example.com/thumb.jpg" {
		t.Errorf("Thumbnail = %q, want thumb.jpg when poster absent", scene.Thumbnail)
	}
}

func TestFmtID(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{float64(123), "123"},
		{float64(0), "0"},
		{"abc", "abc"},
		{nil, "<nil>"},
	}
	for _, tt := range tests {
		got := fmtID(tt.in)
		if got != tt.want {
			t.Errorf("fmtID(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEndToEndPagination(t *testing.T) {
	page1 := buildNuxtData([]map[string]any{
		{"id": float64(10), "cachedSlug": "scene-a", "title": "Scene A", "releasedAt": "2025-01-01T00:00:00.000Z"},
		{"id": float64(11), "cachedSlug": "scene-b", "title": "Scene B", "releasedAt": "2025-01-02T00:00:00.000Z"},
	}, true)
	page2 := buildNuxtData([]map[string]any{
		{"id": float64(12), "cachedSlug": "scene-c", "title": "Scene C", "releasedAt": "2025-01-03T00:00:00.000Z"},
	}, false)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.RawQuery, "page=2"):
			_, _ = fmt.Fprint(w, wrapHTML(page2))
		default:
			_, _ = fmt.Fprint(w, wrapHTML(page1))
		}
	}))
	defer ts.Close()

	domain := strings.TrimPrefix(ts.URL, "http://")
	cfg := SiteConfig{SiteID: "test", Domain: domain, StudioName: "Test"}
	s := New(cfg)

	ctx := context.Background()
	ch, err := s.ListScenes(ctx, ts.URL, scraper.ListOpts{Delay: time.Millisecond})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var scenes []string
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene.Title)
		case scraper.KindError:
			t.Fatalf("unexpected error: %v", res.Err)
		}
	}
	if len(scenes) != 3 {
		t.Errorf("got %d scenes, want 3: %v", len(scenes), scenes)
	}
}

func TestKnownIDsEarlyStop(t *testing.T) {
	page := buildNuxtData([]map[string]any{
		{"id": float64(100), "cachedSlug": "new-scene", "title": "New Scene"},
		{"id": float64(200), "cachedSlug": "old-scene", "title": "Old Scene"},
		{"id": float64(300), "cachedSlug": "older-scene", "title": "Older Scene"},
	}, true)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, wrapHTML(page))
	}))
	defer ts.Close()

	domain := strings.TrimPrefix(ts.URL, "http://")
	cfg := SiteConfig{SiteID: "test", Domain: domain, StudioName: "Test"}
	s := New(cfg)

	ctx := context.Background()
	ch, err := s.ListScenes(ctx, ts.URL, scraper.ListOpts{
		Delay:    time.Millisecond,
		KnownIDs: map[string]bool{"200": true},
	})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var results []scraper.SceneResult
	for res := range ch {
		results = append(results, res)
	}

	sceneCount := 0
	stoppedEarly := false
	for _, r := range results {
		switch r.Kind {
		case scraper.KindScene:
			sceneCount++
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		}
	}
	if sceneCount != 2 {
		t.Errorf("got %d scenes, want 2 — the page is finished before the stop", sceneCount)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
}

func TestModelPageURL(t *testing.T) {
	page := buildModelNuxtData([]map[string]any{
		{"id": float64(50), "cachedSlug": "model-scene", "title": "Model Scene"},
	}, false)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, wrapHTML(page))
	}))
	defer ts.Close()

	domain := strings.TrimPrefix(ts.URL, "http://")
	cfg := SiteConfig{SiteID: "test", Domain: domain, StudioName: "Test"}
	s := New(cfg)

	modelURL := fmt.Sprintf("%s/models/jane-doe", ts.URL)
	ctx := context.Background()
	ch, err := s.ListScenes(ctx, modelURL, scraper.ListOpts{Delay: time.Millisecond})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var scenes []string
	for res := range ch {
		if res.Kind == scraper.KindScene {
			scenes = append(scenes, res.Scene.Title)
		}
	}
	if len(scenes) != 1 {
		t.Errorf("got %d scenes, want 1: %v", len(scenes), scenes)
	}
}

// buildModelNuxtData builds data with tourModelPageData + releases keys.
func buildModelNuxtData(releases []map[string]any, hasNext bool) string {
	flat := make([]any, 0, 64)
	push := func(v any) int { i := len(flat); flat = append(flat, v); return i }

	type releaseRef struct {
		obj map[string]any
	}
	var releaseRefs []releaseRef

	for _, rel := range releases {
		obj := make(map[string]any)
		for k, v := range rel {
			switch val := v.(type) {
			case []any:
				elemRefs := make([]any, len(val))
				for i, elem := range val {
					elemRefs[i] = float64(push(elem))
				}
				arrIdx := push(elemRefs)
				obj[k] = float64(arrIdx)
			case map[string]any:
				inner := make(map[string]any)
				for ik, iv := range val {
					inner[ik] = float64(push(iv))
				}
				obj[k] = float64(push(inner))
			default:
				obj[k] = float64(push(v))
			}
		}
		releaseRefs = append(releaseRefs, releaseRef{obj: obj})
	}

	itemIndices := make([]any, len(releaseRefs))
	for i, rr := range releaseRefs {
		itemIndices[i] = float64(push(rr.obj))
	}
	itemsIdx := push(itemIndices)

	var nextPageVal any
	if hasNext {
		nextPageVal = float64(push("/next"))
	}
	pagIdx := push(map[string]any{"nextPage": nextPageVal})

	sectionIdx := push(map[string]any{
		"items":      float64(itemsIdx),
		"pagination": float64(pagIdx),
	})

	// Model page uses tourModelPageData + releases.
	tourIdx := push(map[string]any{"releases": float64(sectionIdx)})
	dataIdx := push(map[string]any{"tourModelPageData": float64(tourIdx)})
	rootIdx := push(map[string]any{"data": float64(dataIdx)})

	if rootIdx != 0 {
		flat[0], flat[rootIdx] = flat[rootIdx], flat[0]
	}

	b, _ := json.Marshal(flat)
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	cfg := SiteConfig{SiteID: "passionhd", Domain: "passion-hd.com", StudioName: "Passion HD"}
	s := New(cfg)

	tests := []struct {
		url  string
		want bool
	}{
		{"https://passion-hd.com", true},
		{"https://www.passion-hd.com", true},
		{"https://passion-hd.com/video/test", true},
		{"https://passion-hd.com/models/test", true},
		{"https://other-site.com", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestPatterns(t *testing.T) {
	cfg := SiteConfig{SiteID: "passionhd", Domain: "passion-hd.com", StudioName: "Passion HD"}
	s := New(cfg)
	patterns := s.Patterns()
	if len(patterns) != 3 {
		t.Fatalf("got %d patterns, want 3", len(patterns))
	}
	if patterns[0] != "passion-hd.com" {
		t.Errorf("patterns[0] = %q", patterns[0])
	}
}

func TestSceneValidation(t *testing.T) {
	page := buildNuxtData([]map[string]any{
		{
			"id":          float64(999),
			"cachedSlug":  "validated-scene",
			"title":       "Validated Scene",
			"releasedAt":  "2025-06-01T00:00:00.000Z",
			"thumbUrl":    "https://cdn.example.com/thumb.jpg",
			"actors":      []any{map[string]any{"name": "Performer"}},
			"description": "Test description.",
		},
	}, false)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, wrapHTML(page))
	}))
	defer ts.Close()

	domain := strings.TrimPrefix(ts.URL, "http://")
	cfg := SiteConfig{SiteID: "test", Domain: domain, StudioName: "Test"}
	s := New(cfg)

	ctx := context.Background()
	ch, err := s.ListScenes(ctx, ts.URL, scraper.ListOpts{Delay: time.Millisecond})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			testutil.ValidateScene(t, res.Scene)
		case scraper.KindError:
			t.Fatalf("unexpected error: %v", res.Err)
		}
	}
}
