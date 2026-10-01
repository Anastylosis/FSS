package glamose

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

const portalFixture = `
<html><body>
<div class="box">
  <a href="/?update_id=1234"><img data-src="https://cdn.glamose.com/th/1234.jpg" class="play-icon"></a>
  <div class="info">
    <a href="/model/anna-x">Anna &amp; Eve</a>
    <span class="site">Glamose Classic</span>
    <span class="date">3rd Jan 2024</span>
  </div>
</div>
<div class="box">
  <a href="/?update_id=99"><img src="https://cdn.glamose.com/th/99.jpg"></a>
  <div class="info">
    <a href="/model/bella">Bella</a>
    <span class="date">15 January 2023</span>
  </div>
</div>
<div class="box">
  <div class="info"><a href="/model/nobody">No Update Id</a></div>
</div>
</body></html>`

func TestParsePortalPage(t *testing.T) {
	const studioURL = "https://www.glamose.com/"
	scenes := parsePortalPage([]byte(portalFixture), studioURL)

	// The third box carries no update_id and is the scene's only identity, so
	// it must be dropped rather than stored under an empty ID.
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	first := scenes[0]
	if first.ID != "1234" {
		t.Errorf("ID = %q, want 1234", first.ID)
	}
	if first.SiteID != "glamose" || first.Studio != "Glamose" {
		t.Errorf("SiteID/Studio = %q/%q", first.SiteID, first.Studio)
	}
	if first.StudioURL != studioURL {
		t.Errorf("StudioURL = %q, want %q", first.StudioURL, studioURL)
	}
	if want := "https://www.glamose.com/?update_id=1234"; first.URL != want {
		t.Errorf("URL = %q, want %q", first.URL, want)
	}
	if first.Title != "Anna & Eve" {
		t.Errorf("Title = %q, want the unescaped model name", first.Title)
	}
	if len(first.Performers) != 1 || first.Performers[0] != "Anna & Eve" {
		t.Errorf("Performers = %v", first.Performers)
	}
	if first.Series != "Glamose Classic" {
		t.Errorf("Series = %q, want Glamose Classic", first.Series)
	}
	if want := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC); !first.Date.Equal(want) {
		t.Errorf("Date = %v, want %v (ordinal suffix stripped)", first.Date, want)
	}
	if first.Thumbnail != "https://cdn.glamose.com/th/1234.jpg" {
		t.Errorf("Thumbnail = %q", first.Thumbnail)
	}
	if len(first.Tags) != 1 || first.Tags[0] != "Video" {
		t.Errorf("Tags = %v, want [Video] for a box with a play icon", first.Tags)
	}

	second := scenes[1]
	if second.ID != "99" {
		t.Errorf("ID = %q, want 99", second.ID)
	}
	if want := time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC); !second.Date.Equal(want) {
		t.Errorf("Date = %v, want %v (full month layout)", second.Date, want)
	}
	// src= is as valid as data-src=.
	if second.Thumbnail != "https://cdn.glamose.com/th/99.jpg" {
		t.Errorf("Thumbnail = %q", second.Thumbnail)
	}
	if second.Series != "" {
		t.Errorf("Series = %q, want empty when the box has no site span", second.Series)
	}
	if len(second.Tags) != 0 {
		t.Errorf("Tags = %v, want none without a play icon", second.Tags)
	}
}

func TestParsePortalPageEmpty(t *testing.T) {
	if got := parsePortalPage([]byte("<html><body>no boxes</body></html>"), "https://www.glamose.com/"); len(got) != 0 {
		t.Errorf("got %d scenes, want 0", len(got))
	}
}

func TestPortalMatchesURL(t *testing.T) {
	s := &portalScraper{}
	for _, u := range []string{
		"https://www.glamose.com/",
		"https://glamose.com",
		"http://www.glamose.com/?update_id=1",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false, want true", u)
		}
	}
	for _, u := range []string{
		"https://www.glamosetour.com/",
		"https://example.com/glamose.com",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true, want false", u)
		}
	}
}

// currentBox is the portal's live markup: an inline-styled box, a popup link
// through redirect.php, a protocol-relative data-src and a play.png overlay.
func currentBox(id, model, site string, video bool) string {
	play := ""
	if video {
		play = `<img src="/images/icons/play.png" alt="Play Button" width="50" height="31">`
	}
	return fmt.Sprintf(`		<div class="box" style="width:280px;">
		<div id="new_today"><img src="/images/icons/new.png" width="75" height="75" alt="New Today!"></div>        <div style="position:relative;"><div class="popup"><p><a href="/redirect.php?update_id=%[1]s&site_id=18"><img src="/images/updates/transparent.png" data-src="//cdn.glamose.com/updates/%[1]s.webp" alt="%[2]s at %[3]s" width="260" height="390" class="lazyload">%[4]s</a></p></div></div>
        <p><a href="/model/%[2]s" class="title">%[2]s</a><br><span class="site">%[3]s</span><br><span class="date">1st Oct 2026</span></p>
				                </div>
`, id, model, site, play)
}

func currentPage(total int, boxes ...string) string {
	return fmt.Sprintf(`<html><head><title>Glamose Nude Photo & Video Galleries (%d) - Page 1 - Glamose</title></head><body><div id="container">%s</div><div class="pagination"><a href="/?start=30">2</a></div></body></html>`, total, strings.Join(boxes, ""))
}

func TestParsePortalPage_currentMarkup(t *testing.T) {
	body := currentPage(3, currentBox("275606", "Miss_V", "Only Opaques", true), currentBox("275603", "Jorja", "Only Tease", false))
	scenes := parsePortalPage([]byte(body), "https://www.glamose.com/")
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "275606" || sc.Title != "Miss_V" || sc.Series != "Only Opaques" {
		t.Errorf("ID/Title/Series = %q/%q/%q", sc.ID, sc.Title, sc.Series)
	}
	if sc.Thumbnail != "https://cdn.glamose.com/updates/275606.webp" {
		t.Errorf("Thumbnail = %q, want the protocol-relative data-src made absolute", sc.Thumbnail)
	}
	if want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
	if len(sc.Tags) != 1 || sc.Tags[0] != "Video" {
		t.Errorf("Tags = %v, want [Video] for a box with play.png", sc.Tags)
	}
	if len(scenes[1].Tags) != 0 {
		t.Errorf("Tags = %v, want none without a play overlay", scenes[1].Tags)
	}
	if got := parsePortalTotal([]byte(body)); got != 3 {
		t.Errorf("parsePortalTotal = %d, want 3", got)
	}
}

func TestRunPortal_offsetFollowsServedBoxes(t *testing.T) {
	var starts []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("start")
		starts = append(starts, start)
		switch start {
		case "0":
			_, _ = fmt.Fprint(w, currentPage(3, currentBox("3", "A", "S", true), currentBox("2", "B", "S", true)))
		case "2":
			_, _ = fmt.Fprint(w, currentPage(3, currentBox("1", "C", "S", true)))
		default:
			_, _ = fmt.Fprint(w, currentPage(3))
		}
	}))
	defer ts.Close()
	orig := portalBase
	portalBase = ts.URL
	defer func() { portalBase = orig }()

	s := &portalScraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), "https://www.glamose.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	total := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if fmt.Sprint(ids) != "[3 2 1]" {
		t.Errorf("ids = %v, want [3 2 1]", ids)
	}
	if fmt.Sprint(starts) != "[0 2 3]" {
		t.Errorf("start offsets = %v, want [0 2 3]", starts)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
}
