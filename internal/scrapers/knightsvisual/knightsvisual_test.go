package knightsvisual

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func loadJSON[T any](t *testing.T, name string) T {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// newTestServer serves the fixtures as two listing pages (the first padded to
// the REST page size with blog posts) and the include= detail endpoint.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	list := loadJSON[[]listEntry](t, "list.json")
	details := loadJSON[[]post](t, "details.json")

	page1 := append([]listEntry{}, list[:4]...)
	for i := len(page1); i < listPerPage; i++ {
		page1 = append(page1, listEntry{ID: 90000 + i, Link: "https://www.knights-visual.com/blog/filler/post-" + strconv.Itoa(i) + "/"})
	}
	page2 := list[4:]

	byID := map[int]post{}
	for _, p := range details {
		byID[p.ID] = p
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wp-json/wp/v2/posts" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-WP-TotalPages", "2")
		q := r.URL.Query()
		if inc := q.Get("include"); inc != "" {
			var out []post
			for _, s := range strings.Split(inc, ",") {
				id, _ := strconv.Atoi(s)
				out = append(out, byID[id])
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		switch q.Get("page") {
		case "1":
			_ = json.NewEncoder(w).Encode(page1)
		case "2":
			_ = json.NewEncoder(w).Encode(page2)
		default:
			http.Error(w, `{"code":"rest_post_invalid_page_number"}`, http.StatusBadRequest)
		}
	}))
}

func collect(t *testing.T, s *Scraper, studioURL string) []models.Scene {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Fatalf("scrape error: %v", r.Err)
		}
	}
	return scenes
}

func TestListScenesWholeSite(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	s := &Scraper{client: ts.Client(), base: ts.URL}

	scenes := collect(t, s, "https://www.knights-visual.com/")
	if len(scenes) != 6 {
		t.Fatalf("got %d scenes, want 6 (blog filler and the runrun notice are not releases)", len(scenes))
	}

	vr := scenes[0]
	if vr.ID != "FSVR-038" || vr.SiteID != siteID {
		t.Errorf("id/site = %q/%q", vr.ID, vr.SiteID)
	}
	if vr.Title != "3DVR 今日はいっぱいして寝よっ 深月めい" {
		t.Errorf("title = %q", vr.Title)
	}
	if vr.URL != ts.URL+"/works/furasupi/vr/fsvr-038/" {
		t.Errorf("url = %q", vr.URL)
	}
	if vr.StudioURL != "https://www.knights-visual.com/" {
		t.Errorf("studioURL = %q", vr.StudioURL)
	}
	if vr.Studio != "Furasupi" {
		t.Errorf("studio = %q", vr.Studio)
	}
	if vr.Date.Format("2006-01-02") != "2026-09-09" {
		t.Errorf("date = %v", vr.Date)
	}
	if vr.Duration != 68*60 {
		t.Errorf("duration = %d", vr.Duration)
	}
	if len(vr.Performers) != 1 || vr.Performers[0] != "深月めい" {
		t.Errorf("performers = %v", vr.Performers)
	}
	if !strings.HasPrefix(vr.Description, "睡眠前のHをご堪能ください。") {
		t.Errorf("description = %q", vr.Description)
	}
	if vr.Thumbnail != "https://www.knights-visual.com/wp-content/uploads/2026/08/fsvr-038.jpg" {
		t.Errorf("thumbnail = %q", vr.Thumbnail)
	}

	two := scenes[1]
	if two.ID != "KV-269" || len(two.Performers) != 2 || two.Performers[1] != "沙月恵奈" || two.Duration != 130*60 {
		t.Errorf("KV-269 = %+v", two)
	}

	hdd := scenes[2]
	if hdd.ID != "HDDRD-006" || hdd.Studio != "HAKUDAKUDOU" {
		t.Errorf("hakudakudou = %q / %q", hdd.ID, hdd.Studio)
	}
	if len(hdd.Performers) != 3 || hdd.Performers[0] != "ひとみ" || hdd.Performers[2] != "ルカ" {
		t.Errorf("br-separated cast = %v", hdd.Performers)
	}

	studios := map[string]string{}
	for _, sc := range scenes {
		studios[sc.ID] = sc.Studio
	}
	for id, want := range map[string]string{"KV-148": "GLUM", "KV-136": "Paraphilia", "KV-026": "Trigger J"} {
		if studios[id] != want {
			t.Errorf("%s studio = %q, want %q", id, studios[id], want)
		}
	}
}

func TestListScenesLabelFilter(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	s := &Scraper{client: ts.Client(), base: ts.URL}

	tests := []struct {
		url  string
		want []string
	}{
		{"https://www.knights-visual.com/works/furasupi/", []string{"FSVR-038", "KV-269"}},
		{"https://www.knights-visual.com/works/furasupi/oshaburi_gakuen/", []string{"KV-269"}},
		{"https://knights-visual.com/works/hakudakudou", []string{"HDDRD-006"}},
		{"https://www.knights-visual.com/category/works/glum/", []string{"KV-148"}},
		{"https://www.knights-visual.com/works/trigger-j/", []string{"KV-026"}},
		{"https://www.knights-visual.com/works/runrun/", nil},
	}
	for _, tt := range tests {
		scenes := collect(t, s, tt.url)
		var got []string
		for _, sc := range scenes {
			got = append(got, sc.ID)
			if sc.StudioURL != tt.url {
				t.Errorf("%s: StudioURL = %q", tt.url, sc.StudioURL)
			}
		}
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("%s: got %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestKnownIDsStopsWalk(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	s := &Scraper{client: ts.Client(), base: ts.URL}

	ch, err := s.ListScenes(context.Background(), "https://www.knights-visual.com/",
		scraper.ListOpts{KnownIDs: map[string]bool{"KV-269": true}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	stopped := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if !stopped {
		t.Error("expected an early stop")
	}
	if strings.Join(ids, ",") != "FSVR-038,HDDRD-006" {
		t.Errorf("ids = %v; page two should not be reached", ids)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.knights-visual.com/",
		"https://knights-visual.com",
		"https://www.knights-visual.com/works/furasupi/",
		"http://www.knights-visual.com/category/works/glum/",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %q", u)
		}
	}
	for _, u := range []string{
		"https://fskv-samp01.knights-visual.com/x",
		"https://notknights-visual.com/",
		"https://example.com/knights-visual.com",
	} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %q", u)
		}
	}
}

func TestWorksPrefix(t *testing.T) {
	tests := map[string]string{
		"https://www.knights-visual.com/":                            "",
		"https://www.knights-visual.com/works/":                      "",
		"https://www.knights-visual.com/works/Furasupi":              "furasupi/",
		"https://www.knights-visual.com/works/furasupi/nonstop/":     "furasupi/nonstop/",
		"https://www.knights-visual.com/category/works/trigger-j/":   "trigger-j/",
		"https://www.knights-visual.com/blog/furasupi-blog/":         "",
		"https://www.knights-visual.com/works/glum/?utm=1#anchor":    "glum/",
		"https://www.knights-visual.com/category/works/":             "",
		"https://www.knights-visual.com/works/hakudakudou/%E3%81%82": "hakudakudou/あ/",
	}
	for in, want := range tests {
		if got := worksPrefix(in); got != want {
			t.Errorf("worksPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	tests := map[string]int{
		"128分":        128 * 60,
		"5分30秒":       330,
		"180分（3時間0分）": 180 * 60,
		"":            0,
		"unknown":     0,
	}
	for in, want := range tests {
		if got := parseDuration(in); got != want {
			t.Errorf("parseDuration(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestSplitCast(t *testing.T) {
	got := splitCast("永野鈴　福田もも\n花崎こはる、コバ先生")
	want := []string{"永野鈴", "福田もも", "花崎こはる", "コバ先生"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTableWrappedCells(t *testing.T) {
	content := `<table><tr><td class="label">商品番号</td>
<td class="data"><span style="vertical-align: inherit;"><span style="vertical-align: inherit;">KV-254</span></span></td></tr></table>`
	if got := parseTable(content)["商品番号"]; got != "KV-254" {
		t.Errorf("got %q", got)
	}
}

func TestBuildSceneSkipsPagesWithoutCatalogueNumber(t *testing.T) {
	s := &Scraper{base: "https://example.test"}
	p := post{Link: "https://www.knights-visual.com/works/runrun/runrunafesta/"}
	p.Content.Rendered = "<p>ルンルンレーベル</p>"
	if _, ok := s.buildScene(p, "x", time.Now()); ok {
		t.Error("a post without 商品番号 must be skipped")
	}
}

func TestStudioNameFallsBackToPageLabel(t *testing.T) {
	if got := studioName("https://www.knights-visual.com/works/maniac/pinky/kv-1/", "マニアック投稿"); got != "マニアック投稿" {
		t.Errorf("got %q", got)
	}
	if got := studioName("https://www.knights-visual.com/works/trigger-j/futanari/kv-1/", "Trigger-J"); got != "Trigger J" {
		t.Errorf("got %q", got)
	}
}
