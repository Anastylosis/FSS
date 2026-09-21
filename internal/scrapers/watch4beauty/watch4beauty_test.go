package watch4beauty

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.watch4beauty.com/", true},
		{"https://watch4beauty.com/", true},
		{"https://www.watch4beauty.com/model/erika-heiss", true},
		{"https://www.watch4beauty.com/updates/sweet-street-sin", true},
		{"https://www.example.com/", false},
		{"https://www.pornhub.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestModelURLParsing(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.watch4beauty.com/model/erika-heiss", "erika-heiss"},
		{"https://www.watch4beauty.com/model/sara-nixxon", "sara-nixxon"},
		{"https://www.watch4beauty.com/", ""},
		{"https://www.watch4beauty.com/updates/sweet-street-sin", ""},
	}
	for _, c := range cases {
		m := modelRe.FindStringSubmatch(c.url)
		got := ""
		if m != nil {
			got = m[1]
		}
		if got != c.want {
			t.Errorf("modelRe(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestToScene(t *testing.T) {
	iss := issue{
		ID:            7899,
		Category:      5,
		Datetime:      "2026-06-09T02:00:00.000Z",
		Title:         "Sweet Street Sin",
		SimpleTitle:   "sweet-street-sin",
		Size:          81,
		Text:          "Erika Heiss looks adorable.",
		Rating:        0.436,
		VideoPresent:  1,
		Tags:          "european, outdoor, public",
		Prefix:        "issues/2026/06/sweet-street-sin",
		CoverMigrated: 1,
		Widecover:     true,
		CoverFiles: map[string]string{
			"wide-blank":  "000-cover-wide-blank",
			"issue-blank": "000-cover-issue-blank",
		},
	}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	got := toScene("https://www.watch4beauty.com/", iss, now)

	if got.ID != "7899" {
		t.Errorf("ID = %q, want %q", got.ID, "7899")
	}
	if got.SiteID != "watch4beauty" {
		t.Errorf("SiteID = %q", got.SiteID)
	}
	if got.Title != "Sweet Street Sin" {
		t.Errorf("Title = %q", got.Title)
	}
	wantURL := "https://www.watch4beauty.com/updates/sweet-street-sin"
	if got.URL != wantURL {
		t.Errorf("URL = %q, want %q", got.URL, wantURL)
	}
	if got.Studio != "Watch4Beauty" {
		t.Errorf("Studio = %q", got.Studio)
	}
	if got.Description != "Erika Heiss looks adorable." {
		t.Errorf("Description = %q", got.Description)
	}
	wantDate := time.Date(2026, 6, 9, 2, 0, 0, 0, time.UTC)
	if !got.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", got.Date, wantDate)
	}
	if len(got.Tags) != 3 || got.Tags[0] != "european" || got.Tags[1] != "outdoor" || got.Tags[2] != "public" {
		t.Errorf("Tags = %v", got.Tags)
	}
	if got.Likes != 43 {
		t.Errorf("Likes = %d, want 43", got.Likes)
	}
	wantThumb := "https://www.watch4beauty.com/api/covers/issues/2026/06/sweet-street-sin/000-cover-wide-blank_900.jpg"
	if got.Thumbnail != wantThumb {
		t.Errorf("Thumbnail = %q, want %q", got.Thumbnail, wantThumb)
	}
	if !got.ScrapedAt.Equal(now) {
		t.Errorf("ScrapedAt = %v", got.ScrapedAt)
	}
}

func TestCoverURL(t *testing.T) {
	t.Run("migrated wide cover", func(t *testing.T) {
		iss := issue{
			Prefix:        "issues/2026/06/sweet-street-sin",
			CoverMigrated: 1,
			CoverFiles:    map[string]string{"wide-blank": "000-cover-wide-blank"},
		}
		got := coverURL(iss)
		want := "https://www.watch4beauty.com/api/covers/issues/2026/06/sweet-street-sin/000-cover-wide-blank_900.jpg"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("migrated issue fallback", func(t *testing.T) {
		iss := issue{
			Prefix:        "issues/2026/06/test",
			CoverMigrated: 1,
			CoverFiles:    map[string]string{"issue-blank": "000-cover-issue-blank"},
		}
		got := coverURL(iss)
		want := "https://www.watch4beauty.com/api/covers/issues/2026/06/test/000-cover-issue-blank_900.jpg"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("legacy cover", func(t *testing.T) {
		iss := issue{
			Datetime:      "2015-03-15T02:00:00.000Z",
			CoverMigrated: 0,
			CoverFiles:    map[string]string{"wide-blank": "000-cover-wide-blank"},
		}
		got := coverURL(iss)
		want := "https://www.watch4beauty.com/api/legacy-covers/production/20150315-issue-cover-900.jpg"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("no cover files", func(t *testing.T) {
		iss := issue{CoverFiles: map[string]string{}}
		if got := coverURL(iss); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
}

func TestFetchIssues(t *testing.T) {
	issues := []issue{
		{ID: 7899, Title: "Scene One", Datetime: "2026-06-09T02:00:00.000Z", SimpleTitle: "scene-one"},
		{ID: 7898, Title: "Scene Two", Datetime: "2026-06-07T02:00:00.000Z", SimpleTitle: "scene-two"},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/issues":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(issues)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), apiBase: ts.URL}
	got, err := s.fetchIssuesFrom(context.Background(), ts.URL+"/api/issues")
	if err != nil {
		t.Fatalf("fetchIssuesFrom error: %v", err)
	}
	if len(got) != 2 || got[0].ID != 7899 || got[1].ID != 7898 {
		t.Errorf("issues = %+v", got)
	}
}

func TestFetchIssueModels(t *testing.T) {
	resp := []issueModelsResp{
		{
			IssueID: 7899,
			Models: []issueModel{
				{ModelID: 1189, ModelNickname: "Erika Heiss"},
				{ModelID: 1190, ModelNickname: "Sara Nixxon"},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/issues/sweet-street-sin/models":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), apiBase: ts.URL}
	got, err := s.fetchIssueModelsFrom(context.Background(), ts.URL+"/api/issues/sweet-street-sin/models")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(got) != 2 || got[0] != "Erika Heiss" || got[1] != "Sara Nixxon" {
		t.Errorf("performers = %v", got)
	}
}

func TestFetchModelUpdates(t *testing.T) {
	resp := []modelUpdatesResp{
		{
			ModelID:       1189,
			ModelNickname: "Erika Heiss",
			Issues: []issue{
				{ID: 7899, Title: "Sweet Street Sin", Datetime: "2026-06-09T02:00:00.000Z", SimpleTitle: "sweet-street-sin"},
				{ID: 7897, Title: "New Talent", Datetime: "2026-06-05T02:00:00.000Z", SimpleTitle: "new-talent"},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/erika-heiss/updates":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), apiBase: ts.URL}
	got, err := s.fetchModelUpdatesFrom(context.Background(), ts.URL+"/api/models/erika-heiss/updates")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(got) != 1 || got[0].ModelNickname != "Erika Heiss" || len(got[0].Issues) != 2 {
		t.Errorf("result = %+v", got)
	}
}

func TestRunModel(t *testing.T) {
	updatesResp := []modelUpdatesResp{
		{
			ModelID:       1189,
			ModelNickname: "Erika Heiss",
			Issues: []issue{
				{
					ID: 7899, Title: "Sweet Street Sin", SimpleTitle: "sweet-street-sin",
					Datetime: "2026-06-09T02:00:00.000Z", Tags: "outdoor", VideoPresent: 1,
					Prefix: "issues/2026/06/sweet-street-sin", CoverMigrated: 1,
					CoverFiles: map[string]string{"wide-blank": "cover"},
				},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/erika-heiss/updates":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(updatesResp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), apiBase: ts.URL}
	ctx := context.Background()
	out := make(chan scraper.SceneResult, 10)

	s.runModelFrom(ctx, "erika-heiss", ts.URL+"/model/erika-heiss", scraper.ListOpts{}, out, ts.URL+"/api")
	close(out)

	var scenes []string
	for r := range out {
		if r.Kind == scraper.KindScene {
			scenes = append(scenes, r.Scene.Title)
			if len(r.Scene.Performers) != 1 || r.Scene.Performers[0] != "Erika Heiss" {
				t.Errorf("Performers = %v", r.Scene.Performers)
			}
		}
	}
	if len(scenes) != 1 || scenes[0] != "Sweet Street Sin" {
		t.Errorf("scenes = %v", scenes)
	}
}

func TestRunListing(t *testing.T) {
	issues := []issue{
		{
			ID: 7899, Title: "Sweet Street Sin", SimpleTitle: "sweet-street-sin",
			Datetime: "2026-06-09T02:00:00.000Z", Tags: "outdoor", VideoPresent: 1,
			Prefix: "issues/2026/06/sweet-street-sin", CoverMigrated: 1,
			CoverFiles: map[string]string{"wide-blank": "cover"},
		},
	}

	modelsResp := []issueModelsResp{
		{IssueID: 7899, Models: []issueModel{{ModelID: 1189, ModelNickname: "Erika Heiss"}}},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/issues":
			_ = json.NewEncoder(w).Encode(issues)
		case "/api/issues/sweet-street-sin/models":
			_ = json.NewEncoder(w).Encode(modelsResp)
		default:
			_, _ = fmt.Fprint(w, "[]")
		}
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), apiBase: ts.URL}
	ctx := context.Background()
	out := make(chan scraper.SceneResult, 10)

	s.runListingFrom(ctx, ts.URL+"/", scraper.ListOpts{}, out, ts.URL+"/api")
	close(out)

	var scenes []string
	for r := range out {
		if r.Kind == scraper.KindScene {
			scenes = append(scenes, r.Scene.Title)
			if len(r.Scene.Performers) != 1 || r.Scene.Performers[0] != "Erika Heiss" {
				t.Errorf("Performers = %v", r.Scene.Performers)
			}
		}
	}
	if len(scenes) != 1 || scenes[0] != "Sweet Street Sin" {
		t.Errorf("scenes = %v", scenes)
	}
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build `issue` values in Go, so encode and decode share the
// struct tag and a renamed tag round-trips unnoticed. This one decodes a page
// captured verbatim from /api/issues and runs it through toScene, so a tag break
// fails here. Each assertion names the field it pins.
//
// The payload's awkward parts: `issue_tags` is a comma-separated string rather
// than an array, `issue_video_present` is an int flag rather than a bool, and the
// cover URL is assembled from `cdn_host` + `prefix` + a key of the `cover_files`
// map — three fields that must all decode for a thumbnail to exist.
func TestGoldenIssues(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "issues.json"))
	if err != nil {
		t.Fatal(err)
	}
	var issues []issue
	if err := json.Unmarshal(body, &issues); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("decoded %d issues, want 2", len(issues))
	}

	iss := issues[0]
	if iss.ID != 7924 {
		t.Errorf("ID = %d (issue_id), want 7924", iss.ID)
	}
	if iss.Tags != "latina, teen, tattoo, masturbation, dildo" {
		t.Errorf("Tags = %q (issue_tags, comma-separated string)", iss.Tags)
	}
	if iss.CDNHost == "" {
		t.Error("CDNHost is empty (cdn_host)")
	}
	if len(iss.CoverFiles) == 0 {
		t.Error("CoverFiles is empty (cover_files, a map)")
	}

	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	sc := toScene("https://www.watch4beauty.com/", iss, now)

	if sc.ID != "7924" {
		t.Errorf("scene ID = %q (issue_id)", sc.ID)
	}
	if sc.Title != "Pussy Power" {
		t.Errorf("Title = %q (issue_title)", sc.Title)
	}
	if sc.Date.Format("2006-01-02") != "2026-07-25" {
		t.Errorf("Date = %v (issue_datetime), want 2026-07-25", sc.Date)
	}
	if !strings.Contains(sc.URL, "pussy-power") {
		t.Errorf("URL = %q (issue_simple_title)", sc.URL)
	}
	if sc.Description == "" {
		t.Error("Description is empty (issue_text)")
	}
	// issue_tags splits into individual tags.
	var hasLatina bool
	for _, tag := range sc.Tags {
		if tag == "latina" {
			hasLatina = true
		}
	}
	if !hasLatina {
		t.Errorf("Tags = %v (issue_tags split), want latina among them", sc.Tags)
	}
	if sc.Thumbnail == "" {
		t.Error("Thumbnail is empty (cdn_host + prefix + cover_files)")
	}
}

// Roughly half the feed is photo-only issues, which are not scenes. They are
// dropped, and the cursor still walks the unfiltered feed.
func TestPhotoOnlyIssuesAreNotScenes(t *testing.T) {
	issues := []issue{
		{
			ID: 7953, Title: "Photo Set", SimpleTitle: "photo-set",
			Datetime: "2026-09-15T02:00:00.000Z", VideoPresent: 0,
		},
		{
			ID: 7899, Title: "Sweet Street Sin", SimpleTitle: "sweet-street-sin",
			Datetime: "2026-06-09T02:00:00.000Z", VideoPresent: 1,
		},
	}
	modelsResp := []issueModelsResp{
		{IssueID: 7899, Models: []issueModel{{ModelID: 1189, ModelNickname: "Erika Heiss"}}},
	}

	var modelCalls []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/issues":
			_ = json.NewEncoder(w).Encode(issues)
		case "/api/issues/sweet-street-sin/models":
			modelCalls = append(modelCalls, r.URL.Path)
			_ = json.NewEncoder(w).Encode(modelsResp)
		default:
			modelCalls = append(modelCalls, r.URL.Path)
			_, _ = fmt.Fprint(w, "[]")
		}
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), apiBase: ts.URL}
	out := make(chan scraper.SceneResult, 10)
	s.runListingFrom(context.Background(), ts.URL+"/", scraper.ListOpts{Workers: 1}, out, ts.URL+"/api")
	close(out)

	var scenes []string
	for r := range out {
		if r.Kind == scraper.KindScene {
			scenes = append(scenes, r.Scene.Title)
		}
	}
	if len(scenes) != 1 || scenes[0] != "Sweet Street Sin" {
		t.Errorf("scenes = %v, want only the video issue", scenes)
	}
	// The photo set is not fetched for models either.
	for _, p := range modelCalls {
		if p == "/api/issues/photo-set/models" {
			t.Error("fetched models for a photo-only issue")
		}
	}
}

func TestVideoIssuesFilter(t *testing.T) {
	in := []issue{{ID: 1, VideoPresent: 0}, {ID: 2, VideoPresent: 1}, {ID: 3, VideoPresent: 0}}
	got := videoIssues(in)
	if len(got) != 1 || got[0].ID != 2 {
		t.Errorf("videoIssues = %+v", got)
	}
	// The input must not be rewritten under the caller — the cursor still
	// needs the raw page's last entry.
	if len(in) != 3 || in[0].ID != 1 || in[1].ID != 2 {
		t.Errorf("input mutated: %+v", in)
	}
}

// The cursor is a timestamp with a colon and dots in it; it goes into a query
// value, so it is escaped rather than concatenated raw.
func TestListingCursorIsQueryEscaped(t *testing.T) {
	var got []string
	page1 := make([]issue, pageSize)
	for i := range page1 {
		page1[i] = issue{ID: i + 1, SimpleTitle: fmt.Sprintf("s%d", i), VideoPresent: 0,
			Datetime: "2026-06-09T02:00:00.000Z"}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/issues" {
			got = append(got, r.URL.RawQuery)
			if r.URL.RawQuery == "" {
				_ = json.NewEncoder(w).Encode(page1)
				return
			}
			if r.URL.Query().Get("before") != "2026-06-09T02:00:00.000Z" {
				t.Errorf("before = %q", r.URL.Query().Get("before"))
			}
		}
		_, _ = fmt.Fprint(w, "[]")
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), apiBase: ts.URL}
	out := make(chan scraper.SceneResult, pageSize*2)
	s.runListingFrom(context.Background(), ts.URL+"/", scraper.ListOpts{Workers: 1}, out, ts.URL+"/api")
	close(out)

	if len(got) != 2 {
		t.Fatalf("requests = %v, want two pages", got)
	}
	if got[1] != "before=2026-06-09T02%3A00%3A00.000Z" {
		t.Errorf("query = %q", got[1])
	}
}
