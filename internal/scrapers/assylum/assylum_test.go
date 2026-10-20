package assylum

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// sessionPage reproduces a session. The cast and the date share one span, and
// many credits carry an in-fiction role label before the name.
func sessionPage(id int, title, info, desc string) string {
	return fmt.Sprintf(`<html><body class="assylum tour">
<div class="row observation" id="caseContainer" data-lid="%d">
  <div class="ocase clearfix"><div class="lcitem">
    <div class="mainpic"><img src="faceimages/GIfaceimage%d.jpg" alt="" /></div>
    <div class="lch">
      <h3 class="mas_title">%s</h3>
      <span class="lc_info mas_description">%s</span>
    </div>
  </div></div>
  <div class="description"><p class="mas_longdescription">%s</p></div>
</div></body></html>`, id, id, title, info, desc)
}

// indexPage links sessions the way the home and `?sessions` views do: a
// free-text path before the id, which the site does not require.
func indexPage(ids ...int) string {
	var sb strings.Builder
	sb.WriteString(`<html><body>`)
	for _, id := range ids {
		fmt.Fprintf(&sb, `<a href="./session/Some-Performer/Some-Title/%d">x</a>`, id)
	}
	sb.WriteString(`</body></html>`)
	return sb.String()
}

type stubSite struct {
	*httptest.Server
	mu   sync.Mutex
	hits map[string]int
}

func (s *stubSite) hit(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits[p]++
}

func (s *stubSite) count(p string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[p]
}

// newSite serves sessions at `present` only. Absent ids inside the range answer
// a near-empty 200 — the live site's behaviour for a gap — and ids past the end
// answer 404.
func newSite(t *testing.T, present []int, indexed []int) *stubSite {
	t.Helper()
	have := map[int]bool{}
	for _, id := range present {
		have[id] = true
	}
	site := &stubSite{hits: map[string]int{}}
	site.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site.hit(r.URL.Path + "?" + r.URL.RawQuery)
		switch {
		case strings.HasPrefix(r.URL.Path, "/session/"):
			idStr := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			id, _ := strconv.Atoi(idStr)
			if !have[id] {
				if id > 900 {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = fmt.Fprint(w, `<html></html>`)
				return
			}
			_, _ = fmt.Fprint(w, sessionPage(id, fmt.Sprintf("Session %d", id),
				"Patient: Riley Reynolds, Nurse: Hester Prynne, July 24, 2026", "A description."))
		default:
			_, _ = fmt.Fprint(w, indexPage(indexed...))
		}
	}))
	return site
}

func newTestScraper(srv *httptest.Server) *Scraper {
	s := New()
	s.Client = srv.Client()
	s.baseOverride = srv.URL
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, []error, int) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
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

// The index views reach only a fraction of the catalogue, so the sweep runs to
// the highest id they name plus a margin, and finds the ids in between.
func TestSweepFindsSessionsTheIndexDoesNot(t *testing.T) {
	site := newSite(t, []int{3, 7, 12, 20}, []int{12, 20})
	defer site.Close()

	s := newTestScraper(site.Server)
	scenes, errs, _ := collect(t, s, "https://www.assylum.com/", scraper.ListOpts{Workers: 3})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4 — ids 3 and 7 are not linked from any index", len(scenes))
	}
	ids := map[string]bool{}
	for _, sc := range scenes {
		ids[sc.ID] = true
	}
	for _, want := range []string{"3", "7", "12", "20"} {
		if !ids[want] {
			t.Errorf("missing session %s", want)
		}
	}
}

// A gap in the id range answers a near-empty 200 rather than a 404, and must
// not be reported as a failure — most of the sweep lands on gaps.
func TestGapsInTheIDRangeAreNotErrors(t *testing.T) {
	site := newSite(t, []int{5}, []int{5})
	defer site.Close()

	s := newTestScraper(site.Server)
	scenes, errs, _ := collect(t, s, "https://www.assylum.com/", scraper.ListOpts{Workers: 2})
	if len(errs) != 0 {
		t.Fatalf("the empty pages between sessions were reported: %v", errs)
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
}

// Many credits carry an in-fiction role label; keeping it files the same person
// under two names, since her other scenes credit her plainly.
func TestRoleLabelsAreStrippedFromCredits(t *testing.T) {
	d := parseSession(sessionPage(780, "The Evolution of H. Nuria",
		"Patient: Riley Reynolds, Nurse: Hester Prynne, July 24, 2026", "A description."))

	if strings.Join(d.performers, ",") != "Riley Reynolds,Hester Prynne" {
		t.Errorf("performers = %v", d.performers)
	}
	if d.date.Format("2006-01-02") != "2026-07-24" {
		t.Errorf("date = %v", d.date)
	}
	if d.title != "The Evolution of H. Nuria" {
		t.Errorf("title = %q", d.title)
	}
	if d.description != "A description." {
		t.Errorf("description = %q", d.description)
	}
	if d.id != "780" {
		t.Errorf("id = %q", d.id)
	}
}

// A credit with no label is stored as published.
func TestUnlabelledCreditsAreUntouched(t *testing.T) {
	d := parseSession(sessionPage(1, "T", "Nuria Millan, July 24, 2026", "d"))
	if len(d.performers) != 1 || d.performers[0] != "Nuria Millan" {
		t.Errorf("performers = %v", d.performers)
	}
}

// The sweep is by id, not by date, so a stored id says nothing about the ids
// after it — it is skipped, never a stop.
func TestKnownIDsAreSkippedNotStoppedOn(t *testing.T) {
	site := newSite(t, []int{3, 7, 12}, []int{12})
	defer site.Close()

	s := newTestScraper(site.Server)
	scenes, _, _ := collect(t, s, "https://www.assylum.com/",
		scraper.ListOpts{Workers: 2, KnownIDs: map[string]bool{"3": true}})

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want the two unknown ones", len(scenes))
	}
	if site.count("/session//3?") != 0 {
		t.Error("a stored session was re-fetched")
	}
	for _, sc := range scenes {
		if sc.ID == "3" {
			t.Error("the stored session was emitted again")
		}
	}
}

// A single session URL skips the sweep.
func TestSingleSessionURLSkipsTheSweep(t *testing.T) {
	site := newSite(t, []int{780}, []int{780})
	defer site.Close()

	s := newTestScraper(site.Server)
	scenes, errs, total := collect(t, s, "https://www.assylum.com/session//780", scraper.ListOpts{Workers: 1})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 1 || total != 1 {
		t.Fatalf("got %d scenes (total %d), want 1", len(scenes), total)
	}
	if site.count("/?home?") != 0 {
		t.Error("a single-session URL read the index views")
	}
}

// Index views naming no sessions leave the sweep with no bound — a template
// change, not an empty site, and it must be said out loud.
func TestIndexWithNoSessionsIsAParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<html><body>nothing here</body></html>")
	}))
	defer srv.Close()

	s := newTestScraper(srv)
	scenes, errs, _ := collect(t, s, "https://www.assylum.com/", scraper.ListOpts{Workers: 1})
	if len(scenes) != 0 {
		t.Fatalf("got %d scenes", len(scenes))
	}
	if len(errs) == 0 {
		t.Fatal("an index with no sessions reported no error")
	}
	if k := scraper.Classify(errs[len(errs)-1]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://assylum.com",
		"https://www.assylum.com/?home",
		"http://www.assylum.com/session//780",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false, want true", u)
		}
	}
	for _, u := range []string{"https://assylumfan.com/", "https://example.com/assylum.com"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true, want false", u)
		}
	}
}

func TestScenesValidate(t *testing.T) {
	site := newSite(t, []int{3, 7, 12}, []int{12})
	defer site.Close()

	s := newTestScraper(site.Server)
	scenes, _, _ := collect(t, s, "https://www.assylum.com/", scraper.ListOpts{Workers: 2})
	for _, sc := range scenes {
		testutil.ValidateScene(t, sc)
	}
}

func TestContextCancellationStopsTheSweep(t *testing.T) {
	site := newSite(t, []int{500}, []int{500})
	defer site.Close()

	s := newTestScraper(site.Server)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, "https://www.assylum.com/", scraper.ListOpts{Workers: 3, Delay: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	cancel()
	for range ch { //nolint:revive // drain so the goroutines can finish their sends
	}
}
