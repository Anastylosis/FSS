package nakedsword

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	t.Parallel()
	s := New()

	match := []string{
		"https://www.nakedsword.com",
		"https://www.nakedsword.com/",
		"https://nakedsword.com/",
		"http://nakedsword.com/studios/23749/nakedsword-x-rhyheim",
		"https://www.nakedsword.com/movies/286031/x/scene/1",
	}
	for _, u := range match {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false, want true", u)
		}
	}

	noMatch := []string{
		"https://www.falconstudios.com/",  // sibling site, handled by gamma
		"https://www.ragingstallion.com/", // ditto
		"https://nakedswordcash.com/",     // affiliate domain, not the catalogue
		"https://example.com/nakedsword.com",
	}
	for _, u := range noMatch {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true, want false", u)
		}
	}
}

func TestParseStudioID(t *testing.T) {
	t.Parallel()
	tests := []struct{ url, want string }{
		{"https://www.nakedsword.com/studios/23749/nakedsword-x-rhyheim", "23749"},
		{"https://nakedsword.com/studios/16352/amateur-straight-guys", "16352"},
		{"https://www.nakedsword.com/studios/16352", "16352"},
		// Main catalogue — no studio filter.
		{"https://www.nakedsword.com/", ""},
		{"https://www.nakedsword.com/just-added", ""},
		// A studios *index* carries no id and must not be treated as one.
		{"https://www.nakedsword.com/studios", ""},
	}
	for _, tt := range tests {
		if got := parseStudioID(tt.url); got != tt.want {
			t.Errorf("parseStudioID(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

// The slug mirrors the site's own replace(/[^A-Za-z0-9]/g,"-"): every
// non-alphanumeric becomes its own dash, so runs are not collapsed. Getting
// this wrong still resolves (the server redirects) but costs a redirect on
// every scene URL.
func TestSceneURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		movieID int
		title   string
		index   int
		want    string
	}{
		{286031, "Blame It On Rio: Beau's First Gangbang", 1,
			siteBase + "/movies/286031/blame-it-on-rio--beau-s-first-gangbang/scene/1"},
		{3445, "Das Butt", 2, siteBase + "/movies/3445/das-butt/scene/2"},
		{1, "A  B", 1, siteBase + "/movies/1/a--b/scene/1"},
		// No movie id means no canonical page.
		{0, "Whatever", 1, ""},
		{7, "", 3, siteBase + "/movies/7/movie/scene/3"},
	}
	for _, tt := range tests {
		if got := sceneURL(tt.movieID, tt.title, tt.index); got != tt.want {
			t.Errorf("sceneURL(%d, %q, %d) = %q, want %q", tt.movieID, tt.title, tt.index, got, tt.want)
		}
	}
}

// xIdent must be decryptable with the same passphrase, carry the current
// timestamp and the whitelabel property id. The API rejects anything else with
// 403 "Bad Whitelabel Identification".
func TestXIdentRoundTrips(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	hdr, err := xIdent(now)
	if err != nil {
		t.Fatalf("xIdent: %v", err)
	}

	envJSON, err := base64.StdEncoding.DecodeString(hdr)
	if err != nil {
		t.Fatalf("header is not base64: %v", err)
	}
	var env struct {
		Ciphertext string `json:"ciphertext"`
		Salt       string `json:"salt"`
		IV         string `json:"iv"`
	}
	if err := json.Unmarshal(envJSON, &env); err != nil {
		t.Fatalf("envelope is not JSON: %v", err)
	}

	salt, err := hex.DecodeString(env.Salt)
	if err != nil {
		t.Fatalf("salt not hex: %v", err)
	}
	if len(salt) != 256 {
		t.Errorf("salt is %d bytes, want 256", len(salt))
	}
	iv, err := hex.DecodeString(env.IV)
	if err != nil {
		t.Fatalf("iv not hex: %v", err)
	}
	if len(iv) != aes.BlockSize {
		t.Errorf("iv is %d bytes, want %d", len(iv), aes.BlockSize)
	}
	ct, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		t.Fatalf("ciphertext not base64: %v", err)
	}

	key, err := pbkdf2.Key(sha512.New, passphrase, salt, 999, 32)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ct)

	// Strip PKCS#7.
	if len(plain) == 0 {
		t.Fatal("empty plaintext")
	}
	pad := int(plain[len(plain)-1])
	if pad <= 0 || pad > aes.BlockSize || pad > len(plain) {
		t.Fatalf("bad PKCS#7 padding byte %d", pad)
	}
	plain = plain[:len(plain)-pad]

	var got struct {
		Date       int64  `json:"date"`
		PropertyID string `json:"propertyId"`
	}
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatalf("payload is not JSON (%q): %v", plain, err)
	}
	if got.Date != now.UnixMilli() {
		t.Errorf("date = %d, want %d (unix ms)", got.Date, now.UnixMilli())
	}
	if got.PropertyID != propertyID {
		t.Errorf("propertyId = %q, want %q", got.PropertyID, propertyID)
	}
}

// Two calls must not produce the same header: salt and IV are random per call.
func TestXIdentIsSaltedPerCall(t *testing.T) {
	t.Parallel()
	now := time.Now()
	a, err := xIdent(now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := xIdent(now)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two headers for the same instant are identical; salt/iv are not random")
	}
}

func TestToScene(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)

	sc := apiScene{
		ID:               1200476,
		Index:            2,
		StartTimeSeconds: 100,
		EndTimeSeconds:   3048,
		SampleVideo:      "https://cdn.example.com/Samples/Scene/1200476.mp4",
		PublishStart:     "2022-12-09T08:01:00.000000Z",
		CoverImages:      []apiImage{{URL: "https://cdn.example.com/a.jpg"}, {URL: "https://cdn.example.com/b.jpg"}},
		Stars:            []apiStar{{ID: 1, Name: "Gael"}, {ID: 2, Name: " Blessed Boy "}, {ID: 3, Name: "  "}},
		Movie: &apiMovie{
			MovieID:     286031,
			Title:       "Generic Title",
			TitleNs:     "Blame It On Rio",
			Description: "generic copy",
		},
	}

	got := toScene(sc, "https://www.nakedsword.com/", "NakedSword", now)

	if got.ID != "1200476" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.SiteID != siteID {
		t.Errorf("SiteID = %q", got.SiteID)
	}
	// The NakedSword-specific title wins over the generic one.
	if got.Title != "Blame It On Rio" {
		t.Errorf("Title = %q, want the titleNs value", got.Title)
	}
	// descriptionNs is absent here, so it falls back to description.
	if got.Description != "generic copy" {
		t.Errorf("Description = %q", got.Description)
	}
	if want := siteBase + "/movies/286031/blame-it-on-rio/scene/2"; got.URL != want {
		t.Errorf("URL = %q, want %q", got.URL, want)
	}
	// Duration is the segment length, not the whole movie's runtime.
	if got.Duration != 2948 {
		t.Errorf("Duration = %d, want 2948 (end - start)", got.Duration)
	}
	if want := time.Date(2022, 12, 9, 8, 1, 0, 0, time.UTC); !got.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", got.Date, want)
	}
	if got.Thumbnail != "https://cdn.example.com/a.jpg" {
		t.Errorf("Thumbnail = %q, want the first cover image", got.Thumbnail)
	}
	if got.Preview != sc.SampleVideo {
		t.Errorf("Preview = %q", got.Preview)
	}
	// Blank star names are dropped, surrounding whitespace trimmed.
	want := []string{"Gael", "Blessed Boy"}
	if len(got.Performers) != len(want) {
		t.Fatalf("Performers = %v, want %v", got.Performers, want)
	}
	for i := range want {
		if got.Performers[i] != want[i] {
			t.Errorf("Performers[%d] = %q, want %q", i, got.Performers[i], want[i])
		}
	}
}

// A scene whose movie is missing must not panic; it just has no title or URL.
func TestToSceneWithoutMovie(t *testing.T) {
	t.Parallel()
	got := toScene(apiScene{ID: 5}, "https://www.nakedsword.com/", "NakedSword", time.Now())
	if got.ID != "5" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Title != "" || got.URL != "" {
		t.Errorf("Title = %q, URL = %q; want both empty", got.Title, got.URL)
	}
}

// ---- end-to-end against a stub API ----

type stubAPI struct {
	*httptest.Server
	pages       int
	perPageSeen []string
	sortSeen    []string
	studioSeen  []string
}

func newStubAPI(t *testing.T, pages int) *stubAPI {
	t.Helper()
	s := &stubAPI{pages: pages}

	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-ident") == "" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `{"error":"Bad Whitelabel Identification","code":403}`)
			return
		}

		if strings.HasSuffix(r.URL.Path, "/details") {
			_, _ = fmt.Fprint(w, `{"success":true,"data":{"id":23749,"name":"NakedSword X Rhyheim"}}`)
			return
		}

		q := r.URL.Query()
		s.perPageSeen = append(s.perPageSeen, q.Get("per_page"))
		s.sortSeen = append(s.sortSeen, q.Get("sort"))
		s.studioSeen = append(s.studioSeen, q.Get("studios_id"))

		page := 1
		_, _ = fmt.Sscanf(q.Get("page"), "%d", &page)

		var scenes []string
		for i := range 2 {
			id := page*10 + i
			scenes = append(scenes, fmt.Sprintf(`{
				"id": %d, "index": 1,
				"startTimeSeconds": 0, "endTimeSeconds": 60,
				"publish_start": "2024-0%d-01T00:00:00.000000Z",
				"cover_images": [{"url":"https://cdn.example.com/%d.jpg"}],
				"stars": [{"id":1,"name":"Star %d"}],
				"movie": {"movieId": %d, "title": "Movie %d", "description": "d"}
			}`, id, page, id, id, id, id))
		}

		_, _ = fmt.Fprintf(w, `{"success":true,"message":"Success","data":{"scenes":[%s],
			"pagination":{"current_page":%d,"last_page":%d,"total":%d}}}`,
			strings.Join(scenes, ","), page, s.pages, s.pages*2)
	}))

	origAPI := apiBase
	apiBase = s.URL
	t.Cleanup(func() {
		apiBase = origAPI
		s.Close()
	})
	return s
}

func collect(t *testing.T, ch <-chan scraper.SceneResult) ([]models.Scene, []error, int) {
	t.Helper()
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

func TestRunWalksAllPages(t *testing.T) {
	api := newStubAPI(t, 3)

	s := New()
	s.Client = api.Client()
	ch, err := s.ListScenes(context.Background(), "https://www.nakedsword.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, total := collect(t, ch)

	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 6 {
		t.Errorf("got %d scenes, want 6 (3 pages x 2)", len(scenes))
	}
	if total != 6 {
		t.Errorf("progress total = %d, want 6", total)
	}
	// Newest-first is what makes the KnownIDs early-stop valid, and the large
	// page size is what keeps a full walk to ~470 requests instead of ~3900.
	for i, v := range api.sortSeen {
		if v != "newest" {
			t.Errorf("request %d used sort=%q, want newest", i, v)
		}
	}
	for i, v := range api.perPageSeen {
		if v != "100" {
			t.Errorf("request %d used per_page=%q, want 100", i, v)
		}
	}
	// Main catalogue must not send a studio filter.
	for i, v := range api.studioSeen {
		if v != "" {
			t.Errorf("request %d sent studios_id=%q on the main catalogue", i, v)
		}
	}
}

func TestRunStudioPageFiltersAndNamesStudio(t *testing.T) {
	api := newStubAPI(t, 1)

	s := New()
	s.Client = api.Client()
	ch, err := s.ListScenes(context.Background(),
		"https://www.nakedsword.com/studios/23749/nakedsword-x-rhyheim", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collect(t, ch)

	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) == 0 {
		t.Fatal("no scenes")
	}
	for i, v := range api.studioSeen {
		if v != "23749" {
			t.Errorf("request %d sent studios_id=%q, want 23749", i, v)
		}
	}
	// The sub-studio name comes from /studios/{id}/details, not the parent.
	for _, sc := range scenes {
		if sc.Studio != "NakedSword X Rhyheim" {
			t.Errorf("Studio = %q, want the sub-studio name", sc.Studio)
		}
	}
}

func TestRunStopsEarlyOnKnownID(t *testing.T) {
	api := newStubAPI(t, 5)

	s := New()
	s.Client = api.Client()
	ch, err := s.ListScenes(context.Background(), "https://www.nakedsword.com/",
		scraper.ListOpts{KnownIDs: map[string]bool{"20": true}})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collect(t, ch)

	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	// Page 1 yields ids 10,11; page 2 starts with the known id 20, which is
	// skipped while the rest of page 2 is still emitted.
	if len(scenes) != 3 {
		t.Errorf("got %d scenes, want 3 — page 2 is finished before the stop", len(scenes))
	}
	for _, sc := range scenes {
		if sc.ID == "20" {
			t.Error("the known scene must not be emitted")
		}
	}
}

// A 403 means the whitelabel header was rejected — usually a rotated
// passphrase. It must surface as an error, not an empty success, or a --full
// run would wipe the stored scenes.
func TestRunReportsWhitelabelRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"error":"Bad Whitelabel Identification","code":403}`)
	}))
	defer srv.Close()

	orig := apiBase
	apiBase = srv.URL
	defer func() { apiBase = orig }()

	s := New()
	s.Client = srv.Client()
	ch, err := s.ListScenes(context.Background(), "https://www.nakedsword.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collect(t, ch)

	if len(errs) == 0 {
		t.Error("a 403 produced no error; an empty success would let --full wipe the store")
	}
	if len(scenes) != 0 {
		t.Errorf("got %d scenes on a rejected request", len(scenes))
	}
}

// success:false with an empty scene list must not read as a clean empty scrape.
func TestRunReportsAPIFailureEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"success":false,"message":"nope","data":{"scenes":[],"pagination":{}}}`)
	}))
	defer srv.Close()

	orig := apiBase
	apiBase = srv.URL
	defer func() { apiBase = orig }()

	s := New()
	s.Client = srv.Client()
	ch, err := s.ListScenes(context.Background(), "https://www.nakedsword.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collect(t, ch)

	if len(errs) == 0 {
		t.Error("success:false produced no error")
	}
	if len(scenes) != 0 {
		t.Errorf("got %d scenes", len(scenes))
	}
}
