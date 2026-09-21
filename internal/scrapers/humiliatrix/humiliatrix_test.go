package humiliatrix

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// block reproduces the nested-table shape one update takes: the thumbnail
// opens it, and everything else follows before the next thumbnail.
func block(slug, title, blurb, credits, date, preview string) string {
	s := fmt.Sprintf(`
<tr><td colspan="2" rowspan="7"><a href="join.html"><img src="images/%s.gif" alt="" width="384" height="216" border="0"></a></td>
<td colspan="7" valign="bottom" bgcolor="#FAA5D1"><div align="left">
  <div align="left"><span class="header">%s</span></div>
</div></td></tr>
<tr><td colspan="7" align="left" valign="top" bgcolor="#FAA5D1"><p align="left" class="style22">%s</p></td></tr>
<tr><td colspan="7" bgcolor="#FAA5D1" class="style14"><div align="left" class="style23">%s</div></td></tr>`,
		slug, title, blurb, credits)
	if preview != "" {
		s += fmt.Sprintf("\n<tr><td><a href=\"%s\"><img src=\"images/box2012_18.gif\" alt=\"\"></a></td></tr>", preview)
	}
	if date != "" {
		s += fmt.Sprintf("\n<tr><td colspan=\"2\" bgcolor=\"#FAA5D1\"><div align=\"right\"><span class=\"date11\">%s</span></div></td></tr>", date)
	}
	return s
}

func listingHTML() string {
	return `<html><body><table>` +
		block("ashPPT", "You Will Quit the Dating Sites to Be Ashleigh&#39;s Chastity Puppet",
			`&quot;Why are you wasting time on those dating sites?&quot; 4 minutes.`,
			"Princess Ashleigh, loser rejection, manipulative princess, goddess worship...",
			"June 1 2019", "videopreview/ashPPTprv-1080.mp4") +
		block("remPNA", "Prove to Princess Remi That She Controls You",
			`&quot;What a coward you are.&quot; 6 minutes`,
			"Princess Remi, cum control, humiliation assignment",
			"May 27 2019", "") +
		block("vikDOG", "Goddess Vika Prepares You to Fly in Cargo",
			`<span data-mce-style="font-size: medium;">&quot;You&#39;ll be flying in cargo.&quot;</span> 13 minutes`,
			"Goddess Vika, crating punishment, pet humiliation",
			"", "videopreview/vikDOGprv-1080.mp4") +
		`</table></body></html>`
}

func newTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == listingPath {
			_, _ = fmt.Fprint(w, listingHTML())
			return
		}
		http.NotFound(w, r)
	}))
}

func newTestScraper(ts *httptest.Server) *Scraper {
	return &Scraper{client: ts.Client(), base: ts.URL}
}

func collect(ch <-chan scraper.SceneResult) (scenes []models.Scene, errs, stopped, total int) {
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs++
		case scraper.KindStoppedEarly:
			stopped++
		case scraper.KindTotal:
			total = r.Total
		}
	}
	return scenes, errs, stopped, total
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"http://www.humiliatrix.com/", true},
		{"https://humiliatrix.com", true},
		{"http://www.humiliatrix.com/recentupdates.html", true},
		{"http://humiliatrix.com.evil.net/", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestParseListing(t *testing.T) {
	const base = "http://www.humiliatrix.com"
	pageURL := base + listingPath
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	scenes := parseListing(listingHTML(), base, pageURL, base+"/", now)

	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "ashPPT" {
		t.Errorf("id = %q", sc.ID)
	}
	if sc.Title != "You Will Quit the Dating Sites to Be Ashleigh's Chastity Puppet" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.URL != pageURL+"#ashPPT" {
		t.Errorf("url = %q", sc.URL)
	}
	if sc.Thumbnail != base+"/images/ashPPT.gif" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if sc.Preview != base+"/videopreview/ashPPTprv-1080.mp4" {
		t.Errorf("preview = %q", sc.Preview)
	}
	if sc.Duration != 240 {
		t.Errorf("duration = %d, want 240", sc.Duration)
	}
	if sc.Description != `"Why are you wasting time on those dating sites?"` {
		t.Errorf("description = %q", sc.Description)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Princess Ashleigh" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if len(sc.Tags) != 3 || sc.Tags[0] != "loser rejection" || sc.Tags[2] != "goddess worship" {
		t.Errorf("tags = %v", sc.Tags)
	}
	if sc.Date.Format("2006-01-02") != "2019-06-01" {
		t.Errorf("date = %v", sc.Date)
	}

	// Blocks are sliced between thumbnails, so adjacent ones all survive.
	for i, want := range []string{"ashPPT", "remPNA", "vikDOG"} {
		if scenes[i].ID != want {
			t.Errorf("scene %d id = %q, want %q", i, scenes[i].ID, want)
		}
	}
	// Only the newest updates carry a date; the rest must stay zero rather
	// than inherit the previous block's.
	if !scenes[2].Date.IsZero() {
		t.Errorf("undated scene has date %v", scenes[2].Date)
	}
	if scenes[2].Duration != 780 {
		t.Errorf("duration = %d, want 780", scenes[2].Duration)
	}
	if scenes[1].Preview != "" {
		t.Errorf("preview = %q, want empty", scenes[1].Preview)
	}
}

func TestParseCredits(t *testing.T) {
	tests := []struct {
		line       string
		performers []string
		tags       []string
	}{
		{"Princess Remi, cum control, jerk off commands...", []string{"Princess Remi"}, []string{"cum control", "jerk off commands"}},
		{"Goddess Vika", []string{"Goddess Vika"}, nil},
		{"", nil, nil},
	}
	for _, tt := range tests {
		gotP, gotT := parseCredits(tt.line)
		if strings.Join(gotP, "|") != strings.Join(tt.performers, "|") {
			t.Errorf("parseCredits(%q) performers = %v, want %v", tt.line, gotP, tt.performers)
		}
		if strings.Join(gotT, "|") != strings.Join(tt.tags, "|") {
			t.Errorf("parseCredits(%q) tags = %v, want %v", tt.line, gotT, tt.tags)
		}
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _, total := collect(ch)
	if errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	if total != 3 || len(scenes) != 3 {
		t.Fatalf("total = %d, scenes = %d, want 3 and 3", total, len(scenes))
	}
	if !strings.HasPrefix(scenes[0].URL, ts.URL) {
		t.Errorf("URL = %q, want the test server host", scenes[0].URL)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{
		KnownIDs: map[string]bool{"remPNA": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, stopped, _ := collect(ch)
	if len(scenes) != 1 || scenes[0].ID != "ashPPT" {
		t.Errorf("scenes = %v, want only ashPPT", scenes)
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
}

// A page that loads but yields no update blocks is a parser failure, not an
// empty catalogue.
func TestEmptyListingIsAParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body><table></table></body></html>`)
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
			if got := scraper.Classify(r.Err); got != scraper.FailureParse {
				t.Errorf("Classify = %v, want FailureParse", got)
			}
		}
	}
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}
