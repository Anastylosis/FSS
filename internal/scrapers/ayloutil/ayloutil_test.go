package ayloutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestParseFilter(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want Filter
	}{
		{"actor-pornstar", "https://www.babes.com/pornstar/12345/some-star", Filter{Type: FilterActor, ID: 12345}},
		{"actor-model", "https://www.babes.com/model/3911/alexis-fawx", Filter{Type: FilterActor, ID: 3911}},
		{"actor-modelprofile", "https://www.digitalplayground.com/modelprofile/153/anya-olsen", Filter{Type: FilterActor, ID: 153}},
		{"category", "https://www.babes.com/category/79/milf", Filter{Type: FilterTag, ID: 79}},
		{"site", "https://www.brazzers.com/site/12/brazzers-exxtra", Filter{Type: FilterCollection, ID: 12}},
		{"series", "https://www.brazzers.com/series/4567/something", Filter{Type: FilterSeries, ID: 4567}},
		{"all", "https://www.babes.com", Filter{Type: FilterAll}},
		{"unmatched-path", "https://www.babes.com/random/page", Filter{Type: FilterAll}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseFilter(c.url)
			if got != c.want {
				t.Errorf("ParseFilter(%q) = %+v, want %+v", c.url, got, c.want)
			}
		})
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello World", "hello-world"},
		{"Mom's Friend!! 4K", "mom-s-friend-4k"},
		{"   leading and trailing   ", "leading-and-trailing"},
		{"---already-dashed---", "already-dashed"},
		{"NoSpecialChars", "nospecialchars"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseDate(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"2026-01-15T12:00:00+00:00", time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)},
		{"2026-01-15T08:00:00-04:00", time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)},
		{"", time.Time{}},
		{"not-a-date", time.Time{}},
	}
	for _, c := range cases {
		got := ParseDate(c.in)
		if !got.Equal(c.want) {
			t.Errorf("ParseDate(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIsEmptyJSON(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{``, true},
		{`   `, true},
		{`[]`, true},
		{`{}`, true},
		{`null`, true},
		{` [] `, true},
		{`[1]`, false},
		{`{"a":1}`, false},
		{`"x"`, false},
	}
	for _, c := range cases {
		if got := isEmptyJSON(json.RawMessage(c.in)); got != c.want {
			t.Errorf("isEmptyJSON(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestThumbnailURL(t *testing.T) {
	t.Run("picks first available size in priority order", func(t *testing.T) {
		raw := json.RawMessage(`{
			"poster": {
				"0": {
					"md": {"urls": {"default": "https://cdn.example/md.jpg"}},
					"xl": {"urls": {"default": "https://cdn.example/xl.jpg"}}
				}
			}
		}`)
		got := ThumbnailURL(raw)
		if got != "https://cdn.example/xl.jpg" {
			t.Errorf("got %q, want xl URL", got)
		}
	})

	t.Run("falls back through sizes", func(t *testing.T) {
		raw := json.RawMessage(`{"poster":{"0":{"sm":{"urls":{"default":"https://cdn.example/sm.jpg"}}}}}`)
		if got := ThumbnailURL(raw); got != "https://cdn.example/sm.jpg" {
			t.Errorf("got %q, want sm URL", got)
		}
	})

	t.Run("returns empty on missing poster", func(t *testing.T) {
		if got := ThumbnailURL(json.RawMessage(`{"banner":{}}`)); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("returns empty on empty input", func(t *testing.T) {
		if got := ThumbnailURL(json.RawMessage(`[]`)); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("returns empty on malformed JSON", func(t *testing.T) {
		if got := ThumbnailURL(json.RawMessage(`{not json`)); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestPreviewURL(t *testing.T) {
	t.Run("picks highest-resolution mediabook variant", func(t *testing.T) {
		raw := json.RawMessage(`{
			"mediabook": {
				"files": {
					"320p": {"urls": {"view": "https://cdn.example/320.mp4"}},
					"720p": {"urls": {"view": "https://cdn.example/720.mp4"}},
					"480p": {"urls": {"view": "https://cdn.example/480.mp4"}}
				}
			}
		}`)
		if got := PreviewURL(raw); got != "https://cdn.example/720.mp4" {
			t.Errorf("got %q, want 720p URL", got)
		}
	})

	t.Run("returns empty when input is array", func(t *testing.T) {
		if got := PreviewURL(json.RawMessage(`[]`)); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("returns empty when mediabook missing", func(t *testing.T) {
		if got := PreviewURL(json.RawMessage(`{"other":"thing"}`)); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("skips files with empty view URL", func(t *testing.T) {
		raw := json.RawMessage(`{
			"mediabook": {
				"files": {
					"720p": {"urls": {"view": ""}},
					"480p": {"urls": {"view": "https://cdn.example/480.mp4"}}
				}
			}
		}`)
		if got := PreviewURL(raw); got != "https://cdn.example/480.mp4" {
			t.Errorf("got %q, want 480p URL", got)
		}
	})
}

func TestMediaDuration(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"valid", `{"mediabook":{"length":1234}}`, 1234},
		{"missing mediabook", `{"other":"x"}`, 0},
		{"empty input", `{}`, 0},
		{"array input", `[]`, 0},
		{"malformed", `{not json`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MediaDuration(json.RawMessage(c.raw))
			if got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestParseHeight(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"720p", 720},
		{"1080p", 1080},
		{"320", 320},
		{"", 0},
		{"abc", 0},
	}
	for _, c := range cases {
		if got := parseHeight(c.in); got != c.want {
			t.Errorf("parseHeight(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestToScene(t *testing.T) {
	cfg := SiteConfig{
		SiteID:     "babes",
		SiteBase:   "https://www.babes.com",
		StudioName: "Babes",
	}
	rel := Release{
		ID:           42,
		Type:         "scene",
		Title:        "Sample Scene Title",
		Description:  "A description.",
		DateReleased: "2026-01-15T12:00:00+00:00",
		Actors: []Actor{
			{ID: 1, Name: "Alice"},
			{ID: 2, Name: "Bob"},
		},
		Tags: []Tag{
			{ID: 10, Name: "MILF"},
			{ID: 11, Name: "POV"},
		},
		Collections: []Collection{
			{ID: 100, Name: "Premium Series"},
		},
		Stats:     Stats{Likes: 50, Views: 1000},
		RawImages: json.RawMessage(`{"poster":{"0":{"xl":{"urls":{"default":"https://cdn.example/p.jpg"}}}}}`),
		RawVideos: json.RawMessage(`{"mediabook":{"length":1800,"files":{"720p":{"urls":{"view":"https://cdn.example/v.mp4"}}}}}`),
	}
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)

	got := ToScene(cfg, "https://www.babes.com/pornstar/1/alice", rel, now)

	if got.ID != "42" {
		t.Errorf("ID = %q, want 42", got.ID)
	}
	if got.SiteID != "babes" {
		t.Errorf("SiteID = %q, want babes", got.SiteID)
	}
	if got.StudioURL != "https://www.babes.com/pornstar/1/alice" {
		t.Errorf("StudioURL = %q", got.StudioURL)
	}
	if got.Title != "Sample Scene Title" {
		t.Errorf("Title = %q", got.Title)
	}
	wantURL := "https://www.babes.com/video/42/sample-scene-title"
	if got.URL != wantURL {
		t.Errorf("URL = %q, want %q", got.URL, wantURL)
	}
	if got.Date.IsZero() {
		t.Error("Date is zero, expected 2026-01-15")
	}
	if got.Description != "A description." {
		t.Errorf("Description = %q", got.Description)
	}
	if got.Thumbnail != "https://cdn.example/p.jpg" {
		t.Errorf("Thumbnail = %q", got.Thumbnail)
	}
	if got.Preview != "https://cdn.example/v.mp4" {
		t.Errorf("Preview = %q", got.Preview)
	}
	if got.Duration != 1800 {
		t.Errorf("Duration = %d, want 1800", got.Duration)
	}
	if len(got.Performers) != 2 || got.Performers[0] != "Alice" || got.Performers[1] != "Bob" {
		t.Errorf("Performers = %v", got.Performers)
	}
	if got.Studio != "Babes" {
		t.Errorf("Studio = %q", got.Studio)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "MILF" || got.Tags[1] != "POV" {
		t.Errorf("Tags = %v", got.Tags)
	}
	if got.Series != "Premium Series" {
		t.Errorf("Series = %q", got.Series)
	}
	if got.Likes != 50 || got.Views != 1000 {
		t.Errorf("Likes/Views = %d/%d", got.Likes, got.Views)
	}
	if !got.ScrapedAt.Equal(now) {
		t.Errorf("ScrapedAt = %v", got.ScrapedAt)
	}
}

func TestToScene_emptyCollections(t *testing.T) {
	cfg := SiteConfig{SiteID: "babes", SiteBase: "https://www.babes.com", StudioName: "Babes"}
	rel := Release{
		ID: 1, Title: "T", DateReleased: "2026-01-15T12:00:00+00:00",
		RawImages: json.RawMessage(`[]`), RawVideos: json.RawMessage(`[]`),
	}
	got := ToScene(cfg, "https://www.babes.com", rel, time.Now())
	if got.Series != "" {
		t.Errorf("Series should be empty for empty collections, got %q", got.Series)
	}
	if got.Thumbnail != "" || got.Preview != "" || got.Duration != 0 {
		t.Errorf("expected empty media fields, got thumb=%q preview=%q dur=%d", got.Thumbnail, got.Preview, got.Duration)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWarnParseFailure_logsOncePerOnce(t *testing.T) {
	var once sync.Once
	out := captureStderr(t, func() {
		warnParseFailure(&once, "test-site", json.RawMessage(`first-payload`))
		warnParseFailure(&once, "test-site", json.RawMessage(`second-payload`))
		warnParseFailure(&once, "test-site", json.RawMessage(`third-payload`))
	})

	if got := strings.Count(out, "warning: ayloutil"); got != 1 {
		t.Errorf("expected exactly one warning, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "first-payload") {
		t.Errorf("expected first payload to be sampled, got: %s", out)
	}
	if strings.Contains(out, "second-payload") || strings.Contains(out, "third-payload") {
		t.Errorf("subsequent payloads must not be logged, got: %s", out)
	}
	if !strings.Contains(out, "test-site") {
		t.Errorf("expected location label in warning, got: %s", out)
	}
}

func TestWarnParseFailure_truncatesLongPayload(t *testing.T) {
	var once sync.Once
	big := bytes.Repeat([]byte("x"), 500)
	out := captureStderr(t, func() {
		warnParseFailure(&once, "big-site", json.RawMessage(big))
	})

	if !strings.Contains(out, "...(truncated)") {
		t.Errorf("expected truncation marker, got: %s", out)
	}
	// We sample 200 chars; the marker adds 14. Total stderr line is small.
	if len(out) > 600 {
		t.Errorf("warning line should be truncated, got %d bytes:\n%s", len(out), out)
	}
}

func TestWarnParseFailure_independentOnces(t *testing.T) {
	var a, b sync.Once
	out := captureStderr(t, func() {
		warnParseFailure(&a, "site-A", json.RawMessage(`{"a":1}`))
		warnParseFailure(&b, "site-B", json.RawMessage(`{"b":2}`))
	})
	if !strings.Contains(out, "site-A") || !strings.Contains(out, "site-B") {
		t.Errorf("each Once should fire independently, got: %s", out)
	}
}

// ---- HTTP / orchestration tests ----

func makeRelease(id int) Release {
	return Release{
		ID:           id,
		Type:         "scene",
		Title:        fmt.Sprintf("Scene %d", id),
		Description:  fmt.Sprintf("Description for scene %d.", id),
		DateReleased: fmt.Sprintf("2026-01-%02dT12:00:00+00:00", (id%28)+1),
		Actors:       []Actor{{ID: id * 10, Name: fmt.Sprintf("Performer %d", id)}},
		Tags:         []Tag{{ID: id * 100, Name: fmt.Sprintf("Tag%d", id)}},
		Collections:  []Collection{{ID: 1, Name: "Test Collection"}},
		Stats:        Stats{Likes: id, Views: id * 100},
		RawImages:    json.RawMessage(fmt.Sprintf(`{"poster":{"0":{"xl":{"urls":{"default":"https://cdn.test/img/%d.jpg"}}}}}`, id)),
		RawVideos:    json.RawMessage(fmt.Sprintf(`{"mediabook":{"length":%d,"files":{"720p":{"urls":{"view":"https://cdn.test/vid/%d.mp4"}}}}}`, 600+id, id)),
	}
}

func makeSeries(id int, childIDs []int) Release {
	children := make([]Release, len(childIDs))
	for i, cid := range childIDs {
		children[i] = makeRelease(cid)
		children[i].Type = "scene"
	}
	return Release{
		ID:           id,
		Type:         "serie",
		Title:        fmt.Sprintf("Series %d", id),
		DateReleased: "2026-01-15T12:00:00+00:00",
		Children:     children,
		Actors:       []Actor{{ID: 99, Name: "Series Star"}},
		Tags:         []Tag{{ID: 999, Name: "SeriesTag"}},
		RawImages:    json.RawMessage(`{}`),
		RawVideos:    json.RawMessage(`{}`),
	}
}

type testServer struct {
	ts          *httptest.Server
	releases    []Release
	series      []Release
	collections []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
}

func newTestServer(releases []Release) *testServer {
	s := &testServer{releases: releases}
	s.ts = httptest.NewServer(http.HandlerFunc(s.handler))
	return s
}

func (s *testServer) handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		http.SetCookie(w, &http.Cookie{Name: "instance_token", Value: "test-token-123"})
		_, _ = fmt.Fprint(w, "<html>site</html>")

	case "/v2/releases":
		q := r.URL.Query()
		typ := q.Get("type")
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		if limit == 0 {
			limit = 100
		}

		if typ == "serie" {
			s.handleSeries(w, limit, offset)
			return
		}

		filtered := s.filterReleases(q)
		total := len(filtered)

		end := offset + limit
		if end > total {
			end = total
		}
		var page []Release
		if offset < total {
			page = filtered[offset:end]
		}

		resp := ReleasesResponse{
			Meta:   APIMeta{Count: len(page), Total: total},
			Result: page,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)

	case "/v1/collections":
		w.Header().Set("Content-Type", "application/json")
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if limit <= 0 {
			limit = 100
		}
		page := s.collections
		if offset < len(page) {
			page = page[offset:]
		} else {
			page = nil
		}
		if len(page) > limit {
			page = page[:limit]
		}
		result := struct {
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
			Result []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"result"`
		}{Result: page}
		result.Meta.Total = len(s.collections)
		_ = json.NewEncoder(w).Encode(result)

	default:
		w.WriteHeader(404)
	}
}

func (s *testServer) filterReleases(q map[string][]string) []Release {
	actorID := queryInt(q, "actorId")
	collectionID := queryInt(q, "collectionId")
	tagID := queryInt(q, "tagId")

	if actorID == 0 && collectionID == 0 && tagID == 0 {
		return s.releases
	}

	var filtered []Release
	for _, rel := range s.releases {
		if actorID != 0 {
			for _, a := range rel.Actors {
				if a.ID == actorID {
					filtered = append(filtered, rel)
					break
				}
			}
			continue
		}
		if collectionID != 0 {
			for _, c := range rel.Collections {
				if c.ID == collectionID {
					filtered = append(filtered, rel)
					break
				}
			}
			continue
		}
		if tagID != 0 {
			for _, t := range rel.Tags {
				if t.ID == tagID {
					filtered = append(filtered, rel)
					break
				}
			}
			continue
		}
	}
	return filtered
}

func (s *testServer) handleSeries(w http.ResponseWriter, limit, offset int) {
	total := len(s.series)
	end := offset + limit
	if end > total {
		end = total
	}
	var page []Release
	if offset < total {
		page = s.series[offset:end]
	}
	resp := ReleasesResponse{
		Meta:   APIMeta{Count: len(page), Total: total},
		Result: page,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func queryInt(q map[string][]string, key string) int {
	vals := q[key]
	if len(vals) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(vals[0])
	return n
}

func (s *testServer) close() { s.ts.Close() }

func newScraper(ts *testServer) *Scraper {
	return &Scraper{
		Client: ts.ts.Client(),
		cfg: SiteConfig{
			SiteID:     "testsite",
			SiteBase:   ts.ts.URL,
			StudioName: "Test Studio",
		},
		APIHost: ts.ts.URL,
	}
}

// ---- Constructor ----

func TestNew(t *testing.T) {
	s := New(SiteConfig{SiteID: "x", SiteBase: "https://x.com", StudioName: "X"})
	if s.APIHost != DefaultAPIHost {
		t.Errorf("default APIHost = %q, want %q", s.APIHost, DefaultAPIHost)
	}

	s2 := New(SiteConfig{SiteID: "x", SiteBase: "https://x.com", StudioName: "X", APIHost: "https://custom.api"})
	if s2.APIHost != "https://custom.api" {
		t.Errorf("custom APIHost = %q", s2.APIHost)
	}
}

// ---- FetchToken ----

func TestFetchToken(t *testing.T) {
	ts := newTestServer(nil)
	defer ts.close()
	s := newScraper(ts)

	token, err := s.FetchToken(context.Background())
	if err != nil {
		t.Fatalf("FetchToken: %v", err)
	}
	if token != "test-token-123" {
		t.Errorf("token = %q, want test-token-123", token)
	}
}

func TestFetchTokenMissing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "<html>no cookie</html>")
	}))
	defer ts.Close()

	s := &Scraper{
		Client:  ts.Client(),
		cfg:     SiteConfig{SiteBase: ts.URL},
		APIHost: ts.URL,
	}
	_, err := s.FetchToken(context.Background())
	if err == nil {
		t.Error("expected error when instance_token cookie missing")
	}
}

// ---- FetchPage ----

func TestFetchPage(t *testing.T) {
	releases := []Release{makeRelease(1), makeRelease(2), makeRelease(3)}
	ts := newTestServer(releases)
	defer ts.close()
	s := newScraper(ts)

	got, total, err := s.FetchPage(context.Background(), "test-token", Filter{Type: FilterAll}, 0)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(got) != 3 {
		t.Errorf("got %d releases, want 3", len(got))
	}
	if got[0].Title != "Scene 1" {
		t.Errorf("first title = %q", got[0].Title)
	}
}

func TestFetchPageWithActorFilter(t *testing.T) {
	releases := []Release{makeRelease(1), makeRelease(2), makeRelease(3)}
	ts := newTestServer(releases)
	defer ts.close()
	s := newScraper(ts)

	got, total, err := s.FetchPage(context.Background(), "tok", Filter{Type: FilterActor, ID: 10}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(got) != 1 || got[0].ID != 1 {
		t.Errorf("actor filter: got %d results (total %d), want 1 result with ID=1", len(got), total)
	}
}

func TestFetchPageWithTagFilter(t *testing.T) {
	releases := []Release{makeRelease(1), makeRelease(2)}
	ts := newTestServer(releases)
	defer ts.close()
	s := newScraper(ts)

	got, _, err := s.FetchPage(context.Background(), "tok", Filter{Type: FilterTag, ID: 200}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 2 {
		t.Errorf("tag filter: got %v, want scene 2 only", got)
	}
}

// ---- slugify (internal) ----

func TestSlugifyInternal(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Brazzers Exxtra", "brazzers-exxtra"},
		{"Reality Kings", "reality-kings"},
		{"Mom's Lil' Angel", "moms-lil-angel"},
		{"  spaces  ", "spaces"},
		{"", ""},
	}
	for _, c := range cases {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---- resolveCollectionSlug ----

func TestResolveCollectionSlug(t *testing.T) {
	ts := newTestServer(nil)
	ts.collections = []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}{
		{ID: 10, Name: "Brazzers Exxtra"},
		{ID: 20, Name: "Hot And Mean"},
	}
	defer ts.close()
	s := newScraper(ts)

	id, err := s.resolveCollectionSlug(context.Background(), "tok", "brazzers-exxtra")
	if err != nil {
		t.Fatal(err)
	}
	if id != 10 {
		t.Errorf("got ID %d, want 10", id)
	}
}

func TestResolveCollectionSlugNotFound(t *testing.T) {
	ts := newTestServer(nil)
	ts.collections = []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}{
		{ID: 10, Name: "Brazzers Exxtra"},
	}
	defer ts.close()
	s := newScraper(ts)

	_, err := s.resolveCollectionSlug(context.Background(), "tok", "nonexistent")
	if err == nil {
		t.Error("expected error for unknown slug")
	}
}

// ---- ParseFilter extras ----

func TestParseFilterTagQuery(t *testing.T) {
	f := ParseFilter("https://www.babes.com/videos?tags=42")
	if f.Type != FilterTag || f.ID != 42 {
		t.Errorf("got %+v, want FilterTag ID=42", f)
	}
}

func TestParseFilterCollectionSlug(t *testing.T) {
	f := ParseFilter("https://www.brazzers.com/sites/brazzers-exxtra")
	if f.Type != FilterCollection || f.Slug != "brazzers-exxtra" {
		t.Errorf("got %+v, want FilterCollection slug=brazzers-exxtra", f)
	}
}

// ---- Run (end-to-end) ----

func TestRun(t *testing.T) {
	releases := []Release{makeRelease(1), makeRelease(2), makeRelease(3)}
	ts := newTestServer(releases)
	defer ts.close()
	s := newScraper(ts)

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.ts.URL, scraper.ListOpts{}, out)

	results := testutil.CollectScenes(t, out)
	if len(results) != 3 {
		t.Fatalf("got %d scenes, want 3", len(results))
	}
	for _, sc := range results {
		if sc.SiteID != "testsite" {
			t.Errorf("SiteID = %q", sc.SiteID)
		}
		if sc.Studio != "Test Studio" {
			t.Errorf("Studio = %q", sc.Studio)
		}
		if sc.Title == "" {
			t.Error("Title is empty")
		}
		if len(sc.Performers) == 0 {
			t.Error("Performers is empty")
		}
		if sc.Duration == 0 {
			t.Error("Duration is 0")
		}
		if sc.Thumbnail == "" {
			t.Error("Thumbnail is empty")
		}
	}
}

func TestRunKnownIDs(t *testing.T) {
	releases := []Release{makeRelease(1), makeRelease(2), makeRelease(3)}
	ts := newTestServer(releases)
	defer ts.close()
	s := newScraper(ts)

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"2": true},
	}, out)

	results, stoppedEarly := testutil.CollectScenesWithStop(t, out)
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
	if len(results) != 1 || results[0].ID != "1" {
		t.Errorf("got %d scenes, want 1 (ID=1 before known ID=2)", len(results))
	}
}

func TestRunActorFilter(t *testing.T) {
	releases := []Release{makeRelease(1), makeRelease(2), makeRelease(3)}
	ts := newTestServer(releases)
	defer ts.close()
	s := newScraper(ts)

	studioURL := ts.ts.URL + "/pornstar/20/performer-2"
	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), studioURL, scraper.ListOpts{}, out)

	results := testutil.CollectScenes(t, out)
	if len(results) != 1 || results[0].ID != "2" {
		t.Errorf("actor filter: got %d scenes, want 1 with ID=2", len(results))
	}
}

func TestRunCollectionSlug(t *testing.T) {
	r1 := makeRelease(1)
	r1.Collections = []Collection{{ID: 10, Name: "Hot And Mean"}}
	r2 := makeRelease(2)
	r2.Collections = []Collection{{ID: 20, Name: "Other"}}
	ts := newTestServer([]Release{r1, r2})
	ts.collections = []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}{
		{ID: 10, Name: "Hot And Mean"},
		{ID: 20, Name: "Other"},
	}
	defer ts.close()
	s := newScraper(ts)

	studioURL := ts.ts.URL + "/sites/hot-and-mean"
	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), studioURL, scraper.ListOpts{}, out)

	results := testutil.CollectScenes(t, out)
	if len(results) != 1 || results[0].ID != "1" {
		t.Errorf("collection slug filter: got %d scenes, want 1 with ID=1", len(results))
	}
}

func TestRunSeries(t *testing.T) {
	ts := newTestServer(nil)
	ts.series = []Release{makeSeries(100, []int{1, 2})}
	defer ts.close()
	s := newScraper(ts)

	studioURL := ts.ts.URL + "/series/100/test-series"
	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), studioURL, scraper.ListOpts{}, out)

	results := testutil.CollectScenes(t, out)
	if len(results) != 2 {
		t.Fatalf("series: got %d scenes, want 2", len(results))
	}
}

func TestRunSeriesInheritsParentFields(t *testing.T) {
	child := Release{
		ID:    50,
		Type:  "scene",
		Title: "Child Scene",
	}
	parent := Release{
		ID:           100,
		Type:         "serie",
		Title:        "Parent Series",
		DateReleased: "2026-03-01T12:00:00+00:00",
		Description:  "Series description",
		Actors:       []Actor{{ID: 1, Name: "Star"}},
		Tags:         []Tag{{ID: 1, Name: "Inherited"}},
		Collections:  []Collection{{ID: 1, Name: "Col"}},
		Children:     []Release{child},
		RawImages:    json.RawMessage(`{"poster":{"0":{"xl":{"urls":{"default":"https://cdn.test/series.jpg"}}}}}`),
		RawVideos:    json.RawMessage(`{}`),
	}
	ts := newTestServer(nil)
	ts.series = []Release{parent}
	defer ts.close()
	s := newScraper(ts)

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.ts.URL+"/series/100/test", scraper.ListOpts{}, out)

	results := testutil.CollectScenes(t, out)
	if len(results) != 1 {
		t.Fatalf("got %d scenes, want 1", len(results))
	}
	sc := results[0]
	if len(sc.Performers) != 1 || sc.Performers[0] != "Star" {
		t.Errorf("performers = %v, want [Star] (inherited from parent)", sc.Performers)
	}
	if sc.Date.IsZero() {
		t.Error("date should be inherited from parent")
	}
	if sc.Description != "Series description" {
		t.Errorf("description = %q, want inherited", sc.Description)
	}
	if sc.Thumbnail != "https://cdn.test/series.jpg" {
		t.Errorf("thumbnail = %q, want inherited from parent images", sc.Thumbnail)
	}
}

func TestRunPagination(t *testing.T) {
	var releases []Release
	for i := 1; i <= 150; i++ {
		releases = append(releases, makeRelease(i))
	}
	ts := newTestServer(releases)
	defer ts.close()
	s := newScraper(ts)

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.ts.URL, scraper.ListOpts{}, out)

	results := testutil.CollectScenes(t, out)
	if len(results) != 150 {
		t.Errorf("pagination: got %d scenes, want 150 (across 2 pages)", len(results))
	}
}

func TestRunContextCancelled(t *testing.T) {
	releases := []Release{makeRelease(1), makeRelease(2)}
	ts := newTestServer(releases)
	defer ts.close()
	s := newScraper(ts)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := make(chan scraper.SceneResult)
	go s.Run(ctx, ts.ts.URL, scraper.ListOpts{}, out)

	var count int
	for range out {
		count++
	}
	if count > 1 {
		t.Errorf("cancelled context should produce minimal results, got %d", count)
	}
}

// A series is ordered by episode, not by date, so a known id says nothing
// about what comes after it: stopping there hid every later episode. The walk
// now runs to the end and the caller de-duplicates.
func TestRunSeriesIgnoresKnownIDs(t *testing.T) {
	ts := newTestServer(nil)
	ts.series = []Release{makeSeries(100, []int{1, 2, 3})}
	defer ts.close()
	s := newScraper(ts)

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.ts.URL+"/series/100/x", scraper.ListOpts{
		KnownIDs: map[string]bool{"2": true},
	}, out)

	results, stoppedEarly := testutil.CollectScenesWithStop(t, out)
	if stoppedEarly {
		t.Error("series mode must not stop early")
	}
	if len(results) != 3 {
		t.Errorf("got %d scenes, want all 3 episodes", len(results))
	}
}

func TestToSceneCustomScenePath(t *testing.T) {
	cfg := SiteConfig{SiteID: "sp", SiteBase: "https://www.spicevids.com", StudioName: "SpiceVids", ScenePath: "scene"}
	rel := Release{ID: 42, Title: "Test", RawImages: json.RawMessage(`[]`), RawVideos: json.RawMessage(`[]`)}
	sc := ToScene(cfg, "https://www.spicevids.com", rel, time.Now())
	if !strings.Contains(sc.URL, "/scene/42/") {
		t.Errorf("URL = %q, want /scene/42/ path", sc.URL)
	}
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build Release values in Go, so encode and decode share the
// struct tag and a renamed one round-trips unnoticed. This is a byte-verbatim
// slice of a live https://site-api.project1service.com/v2/releases response
// (the first two of 1314 for babes.com; meta.count/total copied from the same
// body), so the wire names are pinned independently of the structs.
//
// No credential is embedded. The API needs an `Instance` token, but that token is
// obtained by a plain GET of the site base and reading the instance_token cookie
// — FetchToken does exactly that — and it travels in a *request* header, so it
// never appears in the response. TestGoldenReleasesCarriesNoToken asserts that
// rather than trusting it.
//
// Shapes a hand-written fixture would not have produced:
//   - `id` (4474211) sits beside a different `spartanId` (4712782). Picking the
//     wrong one still decodes and looks plausible, but re-keys every scene.
//   - `dateReleased` is RFC3339 **with** an explicit +00:00 offset, unlike the
//     bare timestamps some other platforms return.
//   - **`actors[1].name` is "Nikki Nuttz " with a trailing space.** This is real
//     API data, and it is why ToScene and match.MergeScenes both trim: the name
//     is looked up in Stash by exact string, so untrimmed it creates a duplicate
//     performer.
//   - `children` is populated, so Release is recursive.
//   - apostrophes arrive as ' escapes, which a re-encode would rewrite.
func TestGoldenReleases(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "releases.json"))
	if err != nil {
		t.Fatal(err)
	}

	var resp ReleasesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if len(resp.Result) != 2 {
		t.Fatalf("decoded %d releases, want 2", len(resp.Result))
	}
	if resp.Meta.Total != 1314 {
		t.Errorf("Meta.Total = %d (meta.total), want 1314 — pagination depends on it", resp.Meta.Total)
	}

	r := resp.Result[0]
	if r.ID != 4474211 {
		t.Errorf("ID = %d (id), want 4474211 — note the sibling spartanId is 4712782", r.ID)
	}
	if r.Type != "scene" {
		t.Errorf("Type = %q (type)", r.Type)
	}
	if r.Title != "Forbidden Fruit" {
		t.Errorf("Title = %q (title)", r.Title)
	}
	if r.DateReleased != "2021-09-14T12:00:00+00:00" {
		t.Errorf("DateReleased = %q (dateReleased), want RFC3339 with an explicit offset", r.DateReleased)
	}
	if len(r.Actors) != 2 {
		t.Fatalf("Actors has %d entries (actors), want 2", len(r.Actors))
	}
	if len(r.Collections) == 0 || r.Collections[0].Name == "" {
		t.Error("no collections (collections) — Series comes from here")
	}
	if len(r.Tags) == 0 {
		t.Error("no tags (tags)")
	}
	if len(r.Children) == 0 {
		t.Error("children is empty; Release is recursive and the nesting is part of the contract")
	}
	if len(r.RawImages) == 0 {
		t.Error("images (RawImages) is empty — thumbnails are decoded from it lazily")
	}

	// The raw payload really does carry the untrimmed name, so the trim in
	// ToScene is guarding against live data rather than a hypothetical.
	var untrimmed bool
	for _, a := range r.Actors {
		if a.Name != strings.TrimSpace(a.Name) {
			untrimmed = true
		}
	}
	if !untrimmed {
		t.Error("no untrimmed actor name in the fixture — if the API stopped doing this, " +
			"say so here rather than dropping the trim; other Aylo sites still do it")
	}

	// ToScene must not pass it through.
	sc := ToScene(SiteConfig{SiteID: "babes", SiteBase: "https://www.babes.com", StudioName: "Babes"},
		"https://www.babes.com", r, time.Now().UTC())
	for _, p := range sc.Performers {
		if p != strings.TrimSpace(p) {
			t.Errorf("ToScene emitted performer %q with surrounding whitespace; "+
				"stash import looks performers up by exact name and would create a duplicate", p)
		}
	}
}

func TestGoldenReleasesCarriesNoToken(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "releases.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The Instance token is a JWT (277 chars, "eyJ..." prefix) sent as a request
	// header, so it should never appear in a response body. Asserted rather than
	// assumed so a future re-capture cannot quietly commit one — the same guard
	// the stashbox fixture carries.
	//
	// The markers are specific on purpose: an earlier version of this test looked
	// for the bare word "Instance" and tripped on the legitimate group name
	// "Babes_Twistys_Master_Instance" in the payload.
	for _, marker := range []string{"instance_token", "instanceToken", "eyJ0eXAiOiJK"} {
		if bytes.Contains(body, []byte(marker)) {
			t.Errorf("fixture contains %q — re-capture without the credential", marker)
		}
	}
	// Re-encoding through any JSON encoder would rewrite \u0027 to a literal apostrophe,
	// so its presence is evidence the file is still a capture.
	if !bytes.Contains(body, []byte(`\u0027`)) {
		t.Error(`fixture lost the \u0027 escapes — it looks re-encoded rather than captured`)
	}
}

// The API ignores a misspelled sort key and returns releases in arbitrary
// order, which makes the KnownIDs early-stop meaningless across ~40 sites.
func TestFetchPageAsksForNewestFirst(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = fmt.Fprint(w, `{"result":[],"meta":{"total":0}}`)
	}))
	defer srv.Close()

	s := New(SiteConfig{SiteID: "x", StudioName: "X", APIHost: srv.URL})
	s.Client = srv.Client()

	if _, _, err := s.FetchPage(context.Background(), "token", Filter{}, 0); err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if !strings.Contains(query, "orderBy=-dateReleased") {
		t.Errorf("query = %q, want orderBy=-dateReleased", query)
	}
	if strings.Contains(query, "orderby=") {
		t.Errorf("query = %q still carries the lowercase key the API ignores", query)
	}
}

// The API publishes no slug, only the display name, and the tours' own
// derivation is not quite slugify's: Reality Kings serves "Mike's Apartment" at
// /sites/mike-s-apartment, where the apostrophe became a separator rather than
// being dropped. Both spellings must resolve.
func TestResolveCollectionSlugPunctuationSpellings(t *testing.T) {
	ts := newTestServer(nil)
	ts.collections = []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}{
		{ID: 10, Name: "Mike's Apartment"},
		{ID: 20, Name: "Busty & Real"},
	}
	defer ts.close()
	s := newScraper(ts)

	for _, tc := range []struct {
		slug string
		want int
	}{
		{"mike-s-apartment", 10},
		{"mikes-apartment", 10},
		{"mikesapartment", 10},
		{"busty-real", 20},
	} {
		id, err := s.resolveCollectionSlug(context.Background(), "tok", tc.slug)
		if err != nil {
			t.Errorf("%s: %v", tc.slug, err)
			continue
		}
		if id != tc.want {
			t.Errorf("%s = %d, want %d", tc.slug, id, tc.want)
		}
	}
}

// The listing is paged at 100. A brand with more than one page used to have
// every collection past the first page silently unresolvable.
func TestResolveCollectionSlugPagesPastTheFirstHundred(t *testing.T) {
	ts := newTestServer(nil)
	for i := 0; i < 150; i++ {
		ts.collections = append(ts.collections, struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}{ID: 1000 + i, Name: fmt.Sprintf("Collection %d", i)})
	}
	defer ts.close()
	s := newScraper(ts)

	id, err := s.resolveCollectionSlug(context.Background(), "tok", "collection-140")
	if err != nil {
		t.Fatal(err)
	}
	if id != 1140 {
		t.Errorf("got ID %d, want 1140", id)
	}
}
