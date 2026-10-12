package westcoastproductions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://westcoastproductions.com/", true},
		{"https://www.westcoastproductions.com/watch-newest-west-coast-productions-clips-and-scenes.html?studio=257", true},
		{"https://blackmassivecocks.com", true},
		{"http://blackmassivecocks.com/watch-newest-black-massive-cocks-clips-and-scenes.html?page=2", true},
		{"https://www.lewood.com/", false},
		{"https://notwestcoastproductions.com/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestResolveTarget(t *testing.T) {
	cases := []struct {
		in, base, path, filter string
	}{
		{"https://westcoastproductions.com/", "https://westcoastproductions.com", "/watch-newest-west-coast-productions-clips-and-scenes.html", ""},
		{"https://www.blackmassivecocks.com/", "https://blackmassivecocks.com", "/watch-newest-black-massive-cocks-clips-and-scenes.html", ""},
		{"https://westcoastproductions.com/watch-newest-west-coast-productions-clips-and-scenes.html?studio=93426", "https://westcoastproductions.com", "/watch-newest-west-coast-productions-clips-and-scenes.html", "93426"},
	}
	for _, c := range cases {
		got := resolveTarget(c.in)
		if got.base != c.base || got.browsePath != c.path || got.studioFilter != c.filter {
			t.Errorf("resolveTarget(%q) = %+v", c.in, got)
		}
	}
	if got := resolveTarget(cases[2].in).browseURL(3); !strings.HasSuffix(got, "?page=3&studio=93426") {
		t.Errorf("browseURL = %q", got)
	}
}

func TestParseCards(t *testing.T) {
	blocks := cardSplitRe.Split(fixture(t, "listing.html"), -1)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d", len(blocks))
	}
	it, ok := parseCard("https://westcoastproductions.com", blocks[1])
	if !ok {
		t.Fatal("card not parsed")
	}
	if it.id != "1758346" || it.title != "Dani Synclair's 1st Anal" {
		t.Errorf("id/title = %q %q", it.id, it.title)
	}
	if it.url != "https://westcoastproductions.com/1758346/black-massive-cocks-black-ass-anal-booty-6-streaming-scene-video.html" {
		t.Errorf("url = %q", it.url)
	}
	if it.thumb != "https://imgs4cdn.adultempire.com/frame/4995742?w=700&tc=7880" {
		t.Errorf("thumb = %q", it.thumb)
	}
	if len(it.performers) != 1 || it.performers[0] != "Dani Synclair" {
		t.Errorf("performers = %v", it.performers)
	}
	if it.duration != 36*60 {
		t.Errorf("duration = %d", it.duration)
	}
	it2, _ := parseCard("x", blocks[2])
	if it2.id != "1757791" || it2.duration != 16*60 || it2.performers[0] != "Hazel Grace" {
		t.Errorf("card 2 = %+v", it2)
	}
}

func TestParsePerformers(t *testing.T) {
	got := parsePerformers(`<a href="/a">Ann</a>, <a href="/b">Bob &amp; Co</a><a href="/a">Ann</a>`)
	if len(got) != 2 || got[0] != "Ann" || got[1] != "Bob & Co" {
		t.Errorf("links = %v", got)
	}
	got = parsePerformers("Ann &amp; Bob, Cy")
	if len(got) != 3 || got[2] != "Cy" {
		t.Errorf("plain = %v", got)
	}
}

func TestParseDetail(t *testing.T) {
	d := parseDetail(fixture(t, "detail.html"))
	if d.date.Format("2006-01-02") != "2025-09-30" {
		t.Errorf("date = %v", d.date)
	}
	if d.studio != "West Coast Productions" || d.series != "Black Ass Anal Booty" || d.director != "Sid Knox" {
		t.Errorf("detail = %+v", d)
	}
}

func serve(t *testing.T, requests *[]string) *httptest.Server {
	t.Helper()
	listing := fixture(t, "listing.html")
	detail := fixture(t, "detail.html")
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*requests = append(*requests, r.URL.RequestURI())
		mu.Unlock()
		if !strings.Contains(r.Header.Get("Cookie"), "ageConfirmed=true") {
			http.Redirect(w, r, "/AgeConfirmation", http.StatusFound)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/watch-newest-"):
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(listing))
				return
			}
			_, _ = w.Write([]byte("<html></html>"))
		case strings.HasSuffix(r.URL.Path, "-streaming-scene-video.html"):
			_, _ = w.Write([]byte(detail))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	u, _ := url.Parse(ts.URL)
	siteHosts[u.Host] = siteHosts["blackmassivecocks.com"]
	t.Cleanup(func() { delete(siteHosts, u.Host) })
	return ts
}

func TestListScenesEndToEnd(t *testing.T) {
	var reqs []string
	ts := serve(t, &reqs)
	studioURL := ts.URL + "/?studio=257"

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			sc := r.Scene
			got[sc.ID]++
			if sc.StudioURL != studioURL || sc.SiteID != siteID {
				t.Errorf("StudioURL/SiteID = %q %q", sc.StudioURL, sc.SiteID)
			}
			if !strings.HasPrefix(sc.URL, ts.URL+"/") {
				t.Errorf("URL = %q", sc.URL)
			}
			if sc.Date != time.Date(2025, 9, 30, 0, 0, 0, 0, time.UTC) {
				t.Errorf("Date = %v", sc.Date)
			}
			if sc.Studio != "West Coast Productions" || sc.Series != "Black Ass Anal Booty" || sc.Director != "Sid Knox" {
				t.Errorf("detail fields = %q %q %q", sc.Studio, sc.Series, sc.Director)
			}
			if sc.ID == "1758346" && (sc.Title != "Dani Synclair's 1st Anal" || sc.Duration != 2160 || sc.Performers[0] != "Dani Synclair") {
				t.Errorf("scene = %+v", sc)
			}
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if len(got) != 2 || got["1758346"] != 1 || got["1757791"] != 1 {
		t.Fatalf("scenes = %v", got)
	}
	var sawFilter bool
	for _, r := range reqs {
		if strings.HasPrefix(r, "/watch-newest-black-massive-cocks-clips-and-scenes.html?") && strings.Contains(r, "studio=257") {
			sawFilter = true
		}
	}
	if !sawFilter {
		t.Errorf("studio filter not forwarded: %v", reqs)
	}
}
