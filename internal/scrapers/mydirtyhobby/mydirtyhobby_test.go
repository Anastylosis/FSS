package mydirtyhobby

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const testStudioURL = "https://www.mydirtyhobby.com/profil/2517040-Dirty-Tina/videos"

// ---- MatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.mydirtyhobby.com/profil/2517040-Dirty-Tina/videos", true},
		{"https://www.mydirtyhobby.com/profil/2517040-Dirty-Tina", true},
		{"http://mydirtyhobby.com/profil/123-some-user", true},
		{"https://www.manyvids.com/Profile/123", false},
		{"https://www.mydirtyhobby.com/videos/top", false},
		{"", false},
	}
	for _, c := range cases {
		got := s.MatchesURL(c.url)
		if got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// ---- profileParams ----

func TestProfileParams(t *testing.T) {
	cases := []struct {
		url      string
		wantUID  int
		wantNick string
		wantErr  bool
	}{
		{"https://www.mydirtyhobby.com/profil/2517040-Dirty-Tina/videos", 2517040, "Dirty-Tina", false},
		{"https://www.mydirtyhobby.com/profil/999-some-user", 999, "some-user", false},
		{"https://www.manyvids.com/Profile/123", 0, "", true},
	}
	for _, c := range cases {
		uid, nick, err := profileParams(c.url)
		if (err != nil) != c.wantErr {
			t.Errorf("profileParams(%q) error = %v, wantErr %v", c.url, err, c.wantErr)
			continue
		}
		if uid != c.wantUID {
			t.Errorf("profileParams(%q) uid = %d, want %d", c.url, uid, c.wantUID)
		}
		if nick != c.wantNick {
			t.Errorf("profileParams(%q) nick = %q, want %q", c.url, nick, c.wantNick)
		}
	}
}

// ---- parseDuration ----

func TestParseDuration(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"10:47", 647},
		{"1:02:03", 3723},
		{"0:30", 30},
		{"", 0},
	}
	for _, c := range cases {
		got := parseutil.ParseDurationColon(c.input)
		if got != c.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

// ---- toScene ----

func TestToScene(t *testing.T) {
	item := mdhItem{
		UID:                 2517040,
		UVID:                12649291,
		Nick:                "Dirty-Tina",
		Title:               "Public in the sauna",
		Description:         "Some description",
		Thumbnail:           "https://cdn.example.com/thumb.jpg",
		Price:               "971",
		HasDiscount:         false,
		Duration:            "10:47",
		LatestPictureChange: "2025-12-06T11:12:06+01:00",
	}
	now := parseDate("2026-01-01T00:00:00Z")
	scene := toScene(testStudioURL, "https://www.mydirtyhobby.com", 2517040, "Dirty-Tina", item, now)

	if scene.ID != "12649291" {
		t.Errorf("ID = %q, want %q", scene.ID, "12649291")
	}
	if scene.SiteID != "mydirtyhobby" {
		t.Errorf("SiteID = %q, want %q", scene.SiteID, "mydirtyhobby")
	}
	if scene.Title != "Public in the sauna" {
		t.Errorf("Title = %q, want %q", scene.Title, "Public in the sauna")
	}
	if scene.Duration != 647 {
		t.Errorf("Duration = %d, want 647", scene.Duration)
	}
	if len(scene.PriceHistory) != 1 {
		t.Fatalf("PriceHistory len = %d, want 1", len(scene.PriceHistory))
	}
	if scene.PriceHistory[0].Regular != 9.71 {
		t.Errorf("Regular = %f, want 9.71", scene.PriceHistory[0].Regular)
	}
	wantURL := "https://www.mydirtyhobby.com/profil/2517040-Dirty-Tina/videos/12649291"
	if scene.URL != wantURL {
		t.Errorf("URL = %q, want %q", scene.URL, wantURL)
	}
}

// ---- ListScenes (httptest) ----

func makeResponse(items []mdhItem, total, page, totalPages int) []byte {
	resp := listResponse{
		Items:      items,
		Total:      total,
		Page:       page,
		TotalPages: totalPages,
	}
	b, _ := json.Marshal(resp)
	return b
}

func TestListScenes(t *testing.T) {
	page1 := []mdhItem{
		{UID: 1, UVID: 101, Nick: "TestUser", Title: "Scene A", Price: "500", Duration: "5:00", LatestPictureChange: "2025-01-01T00:00:00Z"},
		{UID: 1, UVID: 102, Nick: "TestUser", Title: "Scene B", Price: "600", Duration: "6:00", LatestPictureChange: "2025-01-02T00:00:00Z"},
	}
	page2 := []mdhItem{
		{UID: 1, UVID: 103, Nick: "TestUser", Title: "Scene C", Price: "700", Duration: "7:00", LatestPictureChange: "2025-01-03T00:00:00Z"},
	}

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req listRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if req.Page == 1 {
			_, _ = w.Write(makeResponse(page1, 3, 1, 2))
		} else {
			_, _ = w.Write(makeResponse(page2, 3, 2, 2))
		}
	}))
	defer srv.Close()

	s := &Scraper{
		client:      srv.Client(),
		siteBase:    srv.URL,
		contentBase: srv.URL,
		pageSize:    20,
	}

	studioURL := srv.URL + "/profil/1-TestUser/videos"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var scenes []scraper.SceneResult
	for r := range ch {
		scenes = append(scenes, r)
	}

	// First result is Total hint.
	if scenes[0].Total != 3 {
		t.Errorf("Total hint = %d, want 3", scenes[0].Total)
	}
	scenesOnly := scenes[1:]
	if len(scenesOnly) != 3 {
		t.Errorf("got %d scenes, want 3", len(scenesOnly))
	}
	if scenesOnly[0].Scene.ID != "101" {
		t.Errorf("first scene ID = %q, want %q", scenesOnly[0].Scene.ID, "101")
	}
	if callCount != 2 {
		t.Errorf("page requests = %d, want 2", callCount)
	}
}

// The profile API exposes no sort parameter and returns items roughly
// oldest-first, so stopping at a known ID would hide everything newer behind it.
// The whole listing must be walked regardless of what is already stored.
func TestListScenesIgnoresKnownIDs(t *testing.T) {
	items := []mdhItem{
		{UID: 1, UVID: 201, Nick: "U", Title: "New", Price: "100", Duration: "1:00", LatestPictureChange: "2025-06-01T00:00:00Z"},
		{UID: 1, UVID: 202, Nick: "U", Title: "Known", Price: "100", Duration: "1:00", LatestPictureChange: "2025-05-01T00:00:00Z"},
		{UID: 1, UVID: 203, Nick: "U", Title: "Also known", Price: "100", Duration: "1:00", LatestPictureChange: "2025-04-01T00:00:00Z"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(makeResponse(items, 3, 1, 1))
	}))
	defer srv.Close()

	s := &Scraper{
		client:      srv.Client(),
		siteBase:    srv.URL,
		contentBase: srv.URL,
		pageSize:    20,
	}

	studioURL := srv.URL + "/profil/1-U/videos"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{
		KnownIDs: map[string]bool{"202": true},
	})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var results []scraper.SceneResult
	for r := range ch {
		results = append(results, r)
	}

	scenesOnly := make([]scraper.SceneResult, 0)
	sawStoppedEarly := false
	for _, r := range results {
		if r.Kind == scraper.KindStoppedEarly {
			sawStoppedEarly = true
			continue
		}
		if r.Total == 0 {
			scenesOnly = append(scenesOnly, r)
		}
	}
	if len(scenesOnly) != 3 {
		t.Errorf("got %d scenes, want all 3 (the listing is not date-sorted)", len(scenesOnly))
	}
	if sawStoppedEarly {
		t.Error("must not stop early: a known ID says nothing about what follows it")
	}
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build mdhItem values in Go, so encode and decode share the
// struct tag and a renamed one round-trips unnoticed. This is a byte-verbatim
// slice of a live POST to https://www.mydirtyhobby.com/content/api/v2/videos
// (first two of 1092 items for the Dirty-Tina profile; the three pagination keys
// copied from the same body).
//
// The endpoint needs a JSON POST body plus a `Cookie: AGEGATEPASSED=1` header —
// the age gate, not a credential, so nothing secret is embedded. The response's
// `filters` blob (~8KB of country and category lists) is dropped: listResponse
// decodes only items/total/page/totalPages, the same precedent as gammautil
// dropping `_highlightResult`. Every retained object is unedited.
//
// Shapes a hand-written fixture would have got wrong:
//   - **`price` is a string of cents** ("542", i.e. €5.42). toScene runs it
//     through ParseFloat and divides by 100; writing it as a JSON number or as
//     "5.42" would both pass a hand-built fixture and misprice every scene by
//     100x.
//   - `reducedPercent` is **null** when there is no discount, which is why it is
//     a *int rather than an int.
//   - `duration` is a "M:SS" string, not seconds.
//   - the release date comes from **`latestPictureChange`**, and it carries a
//     **+01:00 offset** rather than a Z — parseDate accepts both, and this pins
//     that the offset form is the one actually served.
//   - `u_id` and `uv_id` sit side by side in snake_case; uv_id is the scene, u_id
//     the profile. Picking the wrong one re-keys every scene to the same value.
func TestGoldenVideos(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "videos.json"))
	if err != nil {
		t.Fatal(err)
	}

	var resp listResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("decoded %d items, want 2", len(resp.Items))
	}
	if resp.Total != 1092 || resp.TotalPages != 546 {
		t.Errorf("Total/TotalPages = %d/%d (total/totalPages), want 1092/546 — the walk needs both",
			resp.Total, resp.TotalPages)
	}

	it := resp.Items[0]
	if it.UVID != 12797791 {
		t.Errorf("UVID = %d (uv_id), want 12797791 — note u_id (%d) is the profile, not the scene",
			it.UVID, it.UID)
	}
	if it.UID != 2517040 {
		t.Errorf("UID = %d (u_id), want 2517040", it.UID)
	}
	if it.Nick != "Dirty-Tina" {
		t.Errorf("Nick = %q (nick)", it.Nick)
	}
	if it.Title == "" {
		t.Error("Title is empty (title)")
	}
	if it.Thumbnail == "" {
		t.Error("Thumbnail is empty (thumbnail)")
	}

	// price as a string of cents.
	if it.Price != "542" {
		t.Errorf("Price = %q (price), want the string \"542\" — cents, as a string", it.Price)
	}
	cents, err := strconv.ParseFloat(it.Price, 64)
	if err != nil {
		t.Fatalf("price %q no longer parses as a number: %v", it.Price, err)
	}
	if got := cents / 100; got != 5.42 {
		t.Errorf("price %q / 100 = %v, want 5.42", it.Price, got)
	}

	// nullable discount.
	if it.ReducedPercent != nil {
		t.Errorf("ReducedPercent = %v (reducedPercent), want nil on an undiscounted item", *it.ReducedPercent)
	}
	if it.HasDiscount {
		t.Error("HasDiscount is true (hasDiscount) on an item with a null reducedPercent")
	}

	if it.Duration != "6:01" {
		t.Errorf("Duration = %q (duration), want a M:SS string rather than seconds", it.Duration)
	}

	// the date source and its offset form.
	if it.LatestPictureChange != "2026-02-17T00:03:06+01:00" {
		t.Errorf("LatestPictureChange = %q (latestPictureChange), want an explicit +01:00 offset",
			it.LatestPictureChange)
	}
	if d := parseDate(it.LatestPictureChange); d.IsZero() {
		t.Errorf("parseDate(%q) returned zero — Scene.Date comes from this field",
			it.LatestPictureChange)
	}
}

// The captured body must stay a capture.
func TestGoldenVideosIsRawCapture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "videos.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"price":"542"`)) {
		t.Error(`fixture lost the quoted "price":"542" form — a re-encode may have made it numeric`)
	}
	if !bytes.Contains(body, []byte(`"reducedPercent":null`)) {
		t.Error("fixture lost the explicit null reducedPercent")
	}
	if bytes.Contains(body, []byte("AGEGATEPASSED")) {
		t.Error("fixture contains the age-gate cookie; it belongs in the request, not the body")
	}
}

// The site's edge occasionally answers this JSON endpoint with an HTML error
// page under HTTP 200. Paginate aborts the walk on any fetch error, so the blip
// cost every remaining page — live-observed on page 18 of 22. httpx.DoJSON
// retries the undecodable body, and the walk must finish intact.
func TestListScenesRetriesHTMLErrorPage(t *testing.T) {
	page1 := []mdhItem{
		{UID: 1, UVID: 101, Nick: "U", Title: "A", Price: "100", Duration: "1:00", LatestPictureChange: "2025-01-01T00:00:00Z"},
	}
	page2 := []mdhItem{
		{UID: 1, UVID: 102, Nick: "U", Title: "B", Price: "100", Duration: "1:00", LatestPictureChange: "2025-01-02T00:00:00Z"},
	}

	var page2Calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req listRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if req.Page == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(makeResponse(page1, 2, 1, 2))
			return
		}
		page2Calls++
		if page2Calls == 1 {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<!DOCTYPE html><html><body>oops</body></html>"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(makeResponse(page2, 2, 2, 2))
	}))
	defer srv.Close()

	s := &Scraper{client: srv.Client(), siteBase: srv.URL, contentBase: srv.URL, pageSize: 20}

	ch, err := s.ListScenes(context.Background(), srv.URL+"/profil/1-U/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var ids []string
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatalf("unexpected error result: %v", r.Err)
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		}
	}
	if len(ids) != 2 {
		t.Errorf("got %d scenes (%v), want both pages", len(ids), ids)
	}
	if page2Calls != 2 {
		t.Errorf("page 2 requests = %d, want 2 (1 HTML + 1 retry)", page2Calls)
	}
}

func TestCookieHeader(t *testing.T) {
	tests := []struct {
		name     string
		operator string
		want     string
	}{
		{"none supplied", "", "AGEGATEPASSED=1"},
		{"appended to the age gate", "KEY=12*34:56:78:1", "AGEGATEPASSED=1; KEY=12*34:56:78:1"},
		{"trailing separator trimmed", " KEY=abc; ", "AGEGATEPASSED=1; KEY=abc"},
		{"whitespace only", "   ", "AGEGATEPASSED=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cookieHeader(tt.operator); got != tt.want {
				t.Errorf("cookieHeader(%q) = %q, want %q", tt.operator, got, tt.want)
			}
		})
	}
}

// The age gate must survive: it is what the site sets from its own button, and
// dropping it for an operator cookie would trade one gate for another.
func TestListScenesSendsOperatorCookie(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(makeResponse([]mdhItem{
			{UID: 1, UVID: 1, Nick: "U", Title: "A", Price: "100", Duration: "1:00", LatestPictureChange: "2025-01-01T00:00:00Z"},
		}, 1, 1, 1))
	}))
	defer srv.Close()

	s := &Scraper{client: srv.Client(), siteBase: srv.URL, contentBase: srv.URL, pageSize: 20}
	ch, err := s.ListScenes(context.Background(), srv.URL+"/profil/1-U/videos", scraper.ListOpts{Cookie: "KEY=abc"})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	for range ch { //nolint:revive // drain so the goroutine can finish its sends
	}

	if want := "AGEGATEPASSED=1; KEY=abc"; got != want {
		t.Errorf("Cookie header = %q, want %q", got, want)
	}
}
