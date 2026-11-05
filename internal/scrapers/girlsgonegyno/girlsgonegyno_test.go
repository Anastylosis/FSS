package girlsgonegyno

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func time0() time.Time { return time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC) }

// ---- fixtures ----

// gridCard renders one fragment-grid card. Cards are split on the
// `<div class='col-sm-4 img-portfolio'>` wrapper.
func gridCard(rid, title, thumb, modelBlock, posted, length, views string) string {
	return fmt.Sprintf(`<div class='col-sm-4 img-portfolio'>
  <a href="?mb=Trailer" data-rid="%s">
    <img class='img-responsive thumbvideo' alt="" src='%s' />
  </a>
  <h4><a href="?mb=Trailer">%s</a></h4>
  <div class="meta">
    <strong>Model: </strong>%s<br>
    <strong>Posted: </strong>%s<br>
    <strong>Length: </strong>%s<br>
    <strong>Views: </strong>%s<br>
  </div>
</div>`, rid, thumb, title, modelBlock, posted, length, views)
}

func fragmentHTML(cards ...string) string {
	var sb strings.Builder
	sb.WriteString(`<div class='row'>`)
	for _, c := range cards {
		sb.WriteString(c)
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

const fragHash = "content/pages/abc123.list.htm"

func wrapperHTML() string {
	return `<html><body><div id="mainbody"></div>
<script>$('#mainbody').load("` + fragHash + `");</script>
</body></html>`
}

func testCfg(base string) SiteConfig {
	return SiteConfig{
		ID:       "girlsgonegyno",
		SiteBase: base,
		Studio:   "GirlsGoneGyno",
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?girlsgonegyno\.com`),
	}
}

func card1() string {
	return gridCard("abc123", "Exam Room One", "https://cdn.example/abc123.jpg",
		`<a href="?m=1">Jane Doe</a>, <a href="?m=2">Mary Sue</a>`,
		"Mon, 2 Jan 2023", "12:34", "1,234")
}

func card2() string {
	return gridCard("def456", "Exam Room Two &amp; Three", "https://cdn.example/def456.jpg",
		`<a href="?m=3">Anna Bell</a>`,
		"Tue, 3 Jan 2023", "08:00", "57")
}

// ---- MatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New(sites[0])
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.girlsgonegyno.com/", true},
		{"http://girlsgonegyno.com/index.php", true},
		{"https://www.captiveclinic.com/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

func TestCaptiveClinicConfigRegistered(t *testing.T) {
	if len(sites) != 3 {
		t.Fatalf("expected 3 configured sites, got %d", len(sites))
	}
	if sites[1].ID != "captiveclinic" {
		t.Errorf("sites[1].ID = %q, want captiveclinic", sites[1].ID)
	}
}

// ---- toScene ----

func TestToScene(t *testing.T) {
	s := New(sites[0])
	sc, ok := s.toScene("https://www.girlsgonegyno.com/", card1(), time0())
	if !ok {
		t.Fatal("toScene returned ok=false")
	}
	if sc.ID != "abc123" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != "girlsgonegyno" {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	wantURL := "https://www.girlsgonegyno.com/?mb=" +
		base64.StdEncoding.EncodeToString([]byte("Trailer||abc123"))
	if sc.URL != wantURL {
		t.Errorf("URL = %q, want %q", sc.URL, wantURL)
	}
	if sc.Title != "Exam Room One" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Thumbnail != "https://cdn.example/abc123.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	want := []string{"Jane Doe", "Mary Sue"}
	if len(sc.Performers) != 2 || sc.Performers[0] != want[0] || sc.Performers[1] != want[1] {
		t.Errorf("Performers = %v, want %v", sc.Performers, want)
	}
	if sc.Date.Year() != 2023 || sc.Date.Month() != 1 || sc.Date.Day() != 2 {
		t.Errorf("Date = %v, want 2023-01-02", sc.Date)
	}
	if sc.Duration != 12*60+34 {
		t.Errorf("Duration = %d, want %d", sc.Duration, 12*60+34)
	}
	if sc.Views != 1234 {
		t.Errorf("Views = %d, want 1234", sc.Views)
	}
	if sc.Studio != "GirlsGoneGyno" {
		t.Errorf("Studio = %q", sc.Studio)
	}
}

func TestToSceneHTMLEntities(t *testing.T) {
	s := New(sites[0])
	sc, ok := s.toScene("https://www.girlsgonegyno.com/", card2(), time0())
	if !ok {
		t.Fatal("toScene returned ok=false")
	}
	if sc.Title != "Exam Room Two & Three" {
		t.Errorf("Title = %q, want decoded entity", sc.Title)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Anna Bell" {
		t.Errorf("Performers = %v", sc.Performers)
	}
}

const leluCard = `
		            <div class='col-sm-4 img-portfolio'>
		                <a href='https://www.lelulove.com/?mb=VHJhaWxlcnx8czN2N2EyeDR5OGwwYzg='>
		                    <img class='img-responsive thumbvideo' width='100%' src='https://www.lelulove.com/content/art/videos/s3v7a2x4y8l0c8.jpg' alt='POV'>
		                </a>
		                <h4 style='margin-top:6px;'>
		                    <a href='https://www.lelulove.com/?mb=VHJhaWxlcnx8czN2N2EyeDR5OGwwYzg='>POV Impregnate Me &amp; Creampie</a>
		                </h4>
		                <p style='margin-top:-6px;'><strong>Posted: </strong>Fri, 20 Nov 2026</p>
		            </div>`

func TestToSceneLeluLove(t *testing.T) {
	s := New(sites[2])
	if s.cfg.PageSize != 9 {
		t.Errorf("PageSize = %d", s.cfg.PageSize)
	}
	card := strings.SplitN(leluCard, "<div class='col-sm-4 img-portfolio'>", 2)[1]
	sc, ok := s.toScene("https://www.lelulove.com/", card, time0())
	if !ok {
		t.Fatal("card rejected")
	}
	if sc.ID != "s3v7a2x4y8l0c8" || sc.Title != "POV Impregnate Me & Creampie" {
		t.Errorf("ID/Title = %q %q", sc.ID, sc.Title)
	}
	if !sc.Date.Equal(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date = %v", sc.Date)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Lelu Love" || !strings.HasSuffix(sc.Thumbnail, "s3v7a2x4y8l0c8.jpg") {
		t.Errorf("scene = %+v", sc)
	}
}

func TestToSceneRejectsNonCard(t *testing.T) {
	s := New(sites[0])
	if _, ok := s.toScene("https://x", `<div>no rid here</div>`, time0()); ok {
		t.Error("expected ok=false when data-rid is missing")
	}
}

// ---- end-to-end (wrapper -> fragment hash -> fragment grid) ----

func TestListScenesEndToEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/index.php":
			_, _ = fmt.Fprint(w, wrapperHTML())
		case "/" + fragHash:
			_, _ = fmt.Fprint(w, fragmentHTML(card1(), card2()))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New(testCfg(ts.URL))

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	got := map[string]string{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			got[r.Scene.ID] = r.Scene.Title
			if r.Scene.Date.IsZero() {
				t.Errorf("Date is zero for %q", r.Scene.Title)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(got), got)
	}
	if got["abc123"] != "Exam Room One" || got["def456"] != "Exam Room Two & Three" {
		t.Errorf("unexpected scenes: %v", got)
	}
}

// Config table integrity — see testutil.CheckSiteTable for what this catches and
// why a config-only table needs it even when the *util carries the parsing tests.
func TestSiteTableIntegrity(t *testing.T) {
	// Patterns are derived from SiteBase by Patterns() rather than stored as a
	// column, so take them from the constructed scraper — that also pins the
	// derivation itself.
	rows := make([]testutil.SiteRow, 0, len(sites))
	for _, c := range sites {
		rows = append(rows, testutil.SiteRow{
			ID: c.ID, Base: c.SiteBase, Studio: c.Studio,
			Patterns: New(c).Patterns(), MatchRe: c.MatchRe,
		})
	}
	testutil.CheckSiteTable(t, rows)
}
