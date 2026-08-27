package fakings

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://fakings.com", true},
		{"https://www.fakings.com", true},
		{"https://fakings.com/videos", true},
		{"https://fakings.com/serie/club-maduras", true},
		{"https://fakings.com/actrices-porno/paula-ortiz", true},
		{"https://fakings.com/categoria/exhibicionismo", true},
		{"https://www.brazzers.com", false},
		{"https://example.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestResolveConfig(t *testing.T) {
	cases := []struct {
		url      string
		wantMode pageMode
		wantBase string
	}{
		{"https://fakings.com", modeVideos, "https://fakings.com/videos"},
		{"https://www.fakings.com", modeVideos, "https://www.fakings.com/videos"},
		{"https://fakings.com/videos", modeVideos, "https://fakings.com/videos"},
		{"https://fakings.com/serie/club-maduras", modeSerie, "https://fakings.com/serie/club-maduras"},
		{"https://fakings.com/actrices-porno/paula-ortiz", modeActress, "https://fakings.com/actrices-porno/paula-ortiz"},
		{"https://fakings.com/categoria/exhibicionismo", modeCategory, "https://fakings.com/categoria/exhibicionismo"},
	}
	for _, c := range cases {
		pc := resolveConfig(c.url)
		if pc.mode != c.wantMode {
			t.Errorf("resolveConfig(%q).mode = %d, want %d", c.url, pc.mode, c.wantMode)
		}
		if pc.baseURL != c.wantBase {
			t.Errorf("resolveConfig(%q).baseURL = %q, want %q", c.url, pc.baseURL, c.wantBase)
		}
	}
}

func TestPageURL(t *testing.T) {
	cases := []struct {
		base string
		page int
		want string
	}{
		{"https://fakings.com/videos", 1, "https://fakings.com/videos"},
		{"https://fakings.com/videos", 2, "https://fakings.com/videos/f/pag:2"},
		{"https://fakings.com/serie/club-maduras", 3, "https://fakings.com/serie/club-maduras/f/pag:3"},
		{"https://fakings.com/categoria/exhibicionismo", 1, "https://fakings.com/categoria/exhibicionismo"},
	}
	for _, c := range cases {
		pc := pageConfig{baseURL: c.base}
		if got := pc.pageURL(c.page); got != c.want {
			t.Errorf("pageURL(%d) = %q, want %q", c.page, got, c.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"46:48", 46*60 + 48},
		{"01:07:36", 1*3600 + 7*60 + 36},
		{"5:00", 5 * 60},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseutil.ParseDurationColon(c.input); got != c.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestTitleCase(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"paula ortiz", "Paula Ortiz"},
		{"anna de ville", "Anna De Ville"},
		{"", ""},
	}
	for _, c := range cases {
		if got := titleCase(c.input); got != c.want {
			t.Errorf("titleCase(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestExtractRSC(t *testing.T) {
	html := []byte(`<html><body>` +
		`<script>self.__next_f.push([1,"hello \"world\""])</script>` +
		`<script>self.__next_f.push([1," more data"])</script>` +
		`</body></html>`)
	got := extractRSC(html)
	want := `hello "world" more data`
	if got != want {
		t.Errorf("extractRSC = %q, want %q", got, want)
	}
}

func TestParseGridVideos(t *testing.T) {
	rsc := `["$","$L38","49687",{"video":{"id":49687,"likes":0,"views":2652,"date":"2026-04-26","title":"Test Video One","product":"fakings","duration":"46:48","slug":"test-video-one","profile":"abc123.jpg","serie":{"title":"Club Maduras","slug":"club-maduras"},"previewFilename":"","type":"premium","screenshot":"def456.jpeg","standardPhoto":"ghi789.jpeg"}}]` +
		"\n" +
		`["$","$L38","49688",{"video":{"id":49688,"likes":5,"views":1000,"date":"2026-04-25","title":"Test Video Two","product":"pepeporn","duration":"01:07:36","slug":"test-video-two","profile":"jkl012.jpg","serie":{"title":"Serie Two","slug":"serie-two"},"previewFilename":"","type":"premium","screenshot":"mno345.jpeg","standardPhoto":"pqr678.jpeg"}}]`

	videos := parseGridVideos(rsc)
	if len(videos) != 2 {
		t.Fatalf("got %d videos, want 2", len(videos))
	}
	if videos[0].ID != 49687 {
		t.Errorf("videos[0].ID = %d, want 49687", videos[0].ID)
	}
	if videos[0].Title != "Test Video One" {
		t.Errorf("videos[0].Title = %q", videos[0].Title)
	}
	if videos[0].Duration != "46:48" {
		t.Errorf("videos[0].Duration = %q", videos[0].Duration)
	}
	if videos[0].Serie == nil || videos[0].Serie.Title != "Club Maduras" {
		t.Errorf("videos[0].Serie = %v", videos[0].Serie)
	}
	if videos[1].ID != 49688 {
		t.Errorf("videos[1].ID = %d, want 49688", videos[1].ID)
	}
	if videos[1].Product != "pepeporn" {
		t.Errorf("videos[1].Product = %q", videos[1].Product)
	}
}

func TestParseGridVideosDedup(t *testing.T) {
	rsc := `{"video":{"id":100,"title":"Dup","product":"fakings","duration":"1:00","slug":"dup","profile":"","serie":null,"previewFilename":"","type":"premium","screenshot":"","standardPhoto":""}}` +
		`{"video":{"id":100,"title":"Dup","product":"fakings","duration":"1:00","slug":"dup","profile":"","serie":null,"previewFilename":"","type":"premium","screenshot":"","standardPhoto":""}}`
	videos := parseGridVideos(rsc)
	if len(videos) != 1 {
		t.Errorf("got %d videos, want 1 (dedup)", len(videos))
	}
}

func TestParseActressVideos(t *testing.T) {
	rsc := `{"name":"Paula Ortiz","videos":[{"id":49687,"likes":0,"views":2652,"date":"2026-04-26","title":"Test Video One","product":"fakings","duration":"46:48","slug":"test-video-one","profile":"abc123.jpg","serie":{"title":"Club Maduras","slug":"club-maduras"},"previewFilename":"","type":"premium","screenshot":"def456.jpeg","standardPhoto":"ghi789.jpeg"},{"id":49688,"likes":5,"views":1000,"date":"2026-04-25","title":"Test Video Two","product":"pepeporn","duration":"01:07:36","slug":"test-video-two","profile":"jkl012.jpg","serie":{"title":"Serie Two","slug":"serie-two"},"previewFilename":"","type":"premium","screenshot":"mno345.jpeg","standardPhoto":"pqr678.jpeg"}]}`

	videos := parseActressVideos(rsc)
	if len(videos) != 2 {
		t.Fatalf("got %d videos, want 2", len(videos))
	}
	if videos[0].ID != 49687 {
		t.Errorf("videos[0].ID = %d, want 49687", videos[0].ID)
	}
	if videos[1].ID != 49688 {
		t.Errorf("videos[1].ID = %d, want 49688", videos[1].ID)
	}
}

func TestParsePagination(t *testing.T) {
	rsc := `{"selectedPage":1,"enableQueryFiltering":false,"enableParamFiltering":true,"total":5080,"take":40}`
	total, take := parsePagination(rsc)
	if total != 5080 {
		t.Errorf("total = %d, want 5080", total)
	}
	if take != 40 {
		t.Errorf("take = %d, want 40", take)
	}
}

func TestParsePaginationMissing(t *testing.T) {
	total, take := parsePagination(`no pagination here`)
	if total != 0 || take != 0 {
		t.Errorf("got total=%d take=%d, want 0,0", total, take)
	}
}

// ---- end-to-end HTML fixtures ----

const gridHTML = `<html><body>` +
	`<script>self.__next_f.push([1,"{\"video\":{\"id\":49687,\"likes\":0,\"views\":2652,\"date\":\"2026-04-26\",\"title\":\"Test Video One\",\"product\":\"fakings\",\"duration\":\"46:48\",\"slug\":\"test-video-one\",\"profile\":\"abc123.jpg\",\"serie\":{\"title\":\"Club Maduras\",\"slug\":\"club-maduras\"},\"previewFilename\":\"\",\"type\":\"premium\",\"screenshot\":\"def456.jpeg\",\"standardPhoto\":\"ghi789.jpeg\"}}"])</script>` +
	`<script>self.__next_f.push([1,"{\"video\":{\"id\":49688,\"likes\":5,\"views\":1000,\"date\":\"2026-04-25\",\"title\":\"Test Video Two\",\"product\":\"pepeporn\",\"duration\":\"01:07:36\",\"slug\":\"test-video-two\",\"profile\":\"jkl012.jpg\",\"serie\":{\"title\":\"Serie Two\",\"slug\":\"serie-two\"},\"previewFilename\":\"\",\"type\":\"premium\",\"screenshot\":\"mno345.jpeg\",\"standardPhoto\":\"pqr678.jpeg\"}}"])</script>` +
	`<script>self.__next_f.push([1,"{\"selectedPage\":1,\"enableQueryFiltering\":false,\"enableParamFiltering\":true,\"total\":80,\"take\":40}"])</script>` +
	`</body></html>`

const actressHTML = `<html><body>` +
	`<script>self.__next_f.push([1,"{\"name\":\"Paula Ortiz\",\"videos\":[{\"id\":49687,\"likes\":0,\"views\":2652,\"date\":\"2026-04-26\",\"title\":\"Test Video One\",\"product\":\"fakings\",\"duration\":\"46:48\",\"slug\":\"test-video-one\",\"profile\":\"abc123.jpg\",\"serie\":{\"title\":\"Club Maduras\",\"slug\":\"club-maduras\"},\"previewFilename\":\"\",\"type\":\"premium\",\"screenshot\":\"def456.jpeg\",\"standardPhoto\":\"ghi789.jpeg\"},{\"id\":49688,\"likes\":5,\"views\":1000,\"date\":\"2026-04-25\",\"title\":\"Test Video Two\",\"product\":\"pepeporn\",\"duration\":\"01:07:36\",\"slug\":\"test-video-two\",\"profile\":\"jkl012.jpg\",\"serie\":{\"title\":\"Serie Two\",\"slug\":\"serie-two\"},\"previewFilename\":\"\",\"type\":\"premium\",\"screenshot\":\"mno345.jpeg\",\"standardPhoto\":\"pqr678.jpeg\"}]}"])</script>` +
	`</body></html>`

// ---- end-to-end tests ----

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/f/pag:") {
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
		} else {
			_, _ = fmt.Fprint(w, gridHTML)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, sc := range scenes {
		if sc.ID == "49687" {
			if sc.Title != "Test Video One" {
				t.Errorf("Title = %q", sc.Title)
			}
			if sc.Duration != 46*60+48 {
				t.Errorf("Duration = %d, want %d", sc.Duration, 46*60+48)
			}
			if sc.Series != "Club Maduras" {
				t.Errorf("Series = %q", sc.Series)
			}
			if sc.Studio != "fakings" {
				t.Errorf("Studio = %q", sc.Studio)
			}
			if sc.Thumbnail != cdnBase+"abc123.jpg" {
				t.Errorf("Thumbnail = %q", sc.Thumbnail)
			}
			if sc.Date.Format("2006-01-02") != "2026-04-26" {
				t.Errorf("Date = %v", sc.Date)
			}
		}
	}
}

// A listing whose pagination block cannot be read must keep walking. Reading a
// regex miss as "last page" truncated the catalogue to page 1, and --full's
// authoritative Save would then delete everything behind it.
func TestListScenesUnreadablePaginationKeepsWalking(t *testing.T) {
	noPager := strings.Replace(gridHTML,
		`<script>self.__next_f.push([1,"{\"selectedPage\":1,\"enableQueryFiltering\":false,\"enableParamFiltering\":true,\"total\":80,\"take\":40}"])</script>`,
		"", 1)
	if noPager == gridHTML {
		t.Fatal("fixture changed: the pagination script was not removed")
	}
	page2 := strings.ReplaceAll(noPager, "49687", "59687")
	page2 = strings.ReplaceAll(page2, "49688", "59688")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/f/pag:2"):
			_, _ = fmt.Fprint(w, page2)
		case strings.Contains(r.URL.Path, "/f/pag:"):
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
		default:
			_, _ = fmt.Fprint(w, noPager)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4 — page 2 must still be walked", len(scenes))
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, gridHTML)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{
		KnownIDs: map[string]bool{"49688": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, ch)

	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(scenes) != 1 || scenes[0].ID != "49687" {
		t.Errorf("got ids %v, want [49687]", scenes)
	}
}

func TestListScenesActress(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, actressHTML)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/actrices-porno/paula-ortiz", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, sc := range scenes {
		if len(sc.Performers) != 1 || sc.Performers[0] != "Paula Ortiz" {
			t.Errorf("Performers = %v, want [Paula Ortiz]", sc.Performers)
		}
	}
}
