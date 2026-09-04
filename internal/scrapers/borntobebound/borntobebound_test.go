package borntobebound

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://borntobebound.com/", true},
		{"https://www.borntobebound.com/updates", true},
		{"http://borntobebound.com/updates/?cat=116", true},
		{"https://borntobebound.com/updates/?tag=armbinder", true},
		{"https://boundhoneys.com/", false},
		{"https://notborntobebound.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestHasVideo(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`<a href="/source/clip.mp4">download</a>`, true},
		{`<p>run time 37 minutes</p>`, true},
		{`<p>RUN TIME: 40:06</p>`, true},
		{`<a href="/old/clip.wmv">download</a>`, true},
		{`<p>Journal update: hello everyone</p>`, false},
		{`<img src="photo.jpg"/>`, false},
	}
	for _, c := range cases {
		if got := hasVideo(c.body); got != c.want {
			t.Errorf("hasVideo(%q) = %v, want %v", c.body, got, c.want)
		}
	}
}

func TestDuration(t *testing.T) {
	cases := []struct {
		body string
		want int
	}{
		{`<p>run time 37 minutes</p>`, 37 * 60},
		{`<p>run time: 40:06</p>`, 40*60 + 6},
		{`<p>run time 1:02:30</p>`, 3600 + 2*60 + 30},
		{`<p>run time is about 22 min</p>`, 22 * 60},
		{`<p>run time 1 hour 15 minutes</p>`, 4500},
		{`<p>run time 2 hours</p>`, 7200},
		{`<p>no duration here</p>`, 0},
	}
	for _, c := range cases {
		if got := duration(c.body); got != c.want {
			t.Errorf("duration(%q) = %d, want %d", c.body, got, c.want)
		}
	}
}

func TestThumbnail(t *testing.T) {
	body := `<dl class='gallery-item'><dt><a href='https://borntobebound.com/updates/wp-content/uploads/2026/06/a.jpg'>` +
		`<img src="https://borntobebound.com/updates/wp-content/uploads/2026/06/a-150x150.jpg"/></a></dt></dl>` +
		`<a href='https://borntobebound.com/updates/wp-content/uploads/2026/06/b.jpg'>x</a>`
	want := "https://borntobebound.com/updates/wp-content/uploads/2026/06/a.jpg"
	if got := thumbnail(body); got != want {
		t.Errorf("thumbnail = %q, want %q", got, want)
	}
	if got := thumbnail(`<p>no images</p>`); got != "" {
		t.Errorf("thumbnail = %q, want empty", got)
	}

	// Galleries that link nothing full-size still carry the resized <img>.
	imgOnly := `<dl class='gallery-item'><dt><img width="150" src="https://borntobebound.com/updates/wp-content/uploads/2026/06/a-150x150.jpg"/></dt></dl>`
	wantImg := "https://borntobebound.com/updates/wp-content/uploads/2026/06/a-150x150.jpg"
	if got := thumbnail(imgOnly); got != wantImg {
		t.Errorf("thumbnail = %q, want %q", got, wantImg)
	}
}

func TestDescription(t *testing.T) {
	body := `<div class='gallery'><img src="a.jpg"/></div>` +
		`<p style="text-align: center;"><a href="/source/x.mp4">members right click here to download this mp4 or left click to stream</a></p>` + "\n" +
		`<p style="text-align: center;">run time 37 minutes</p>` + "\n" +
		`<p style="text-align: center;">Two&nbsp;milfy coworkers have a big plan.</p>` + "\n" +
		`<p>Did they make it out?</p>`
	got := description(body)
	want := "Two milfy coworkers have a big plan.\n\nDid they make it out?"
	if got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestStarring(t *testing.T) {
	cases := []struct {
		title string
		want  []string
	}{
		{"Coworkers plan derailed starring Carissa Dumond and JJ Plush", []string{"Carissa Dumond", "JJ Plush"}},
		{"A tie starring Ariel Anderssen, Sahrye and JJ", []string{"Ariel Anderssen", "Sahrye", "JJ"}},
		{"No cast in this title", nil},
	}
	for _, c := range cases {
		got := starring(c.title)
		if !slices.Equal(got, c.want) {
			t.Errorf("starring(%q) = %v, want %v", c.title, got, c.want)
		}
	}
}

func TestCleanText(t *testing.T) {
	in := "Jayda&#8217;s   <em>barefoot</em>&nbsp;bondage fun"
	want := "Jayda’s barefoot bondage fun"
	if got := cleanText(in); got != want {
		t.Errorf("cleanText = %q, want %q", got, want)
	}
}

// ---- end-to-end over a fake WordPress REST API ----

type fakeWP struct {
	posts    []wpPost
	tags     map[int]string
	cats     map[int]string
	pageHits []int
	termReqs int
}

func (f *fakeWP) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		switch q.Get("rest_route") {
		case "/wp/v2/posts":
			page, _ := strconv.Atoi(q.Get("page"))
			per, _ := strconv.Atoi(q.Get("per_page"))
			f.pageHits = append(f.pageHits, page)
			start := (page - 1) * per
			if start >= len(f.posts) {
				_, _ = fmt.Fprint(w, "[]")
				return
			}
			end := min(start+per, len(f.posts))
			_ = json.NewEncoder(w).Encode(f.posts[start:end])
		case "/wp/v2/tags", "/wp/v2/categories":
			f.termReqs++
			src := f.tags
			if q.Get("rest_route") == "/wp/v2/categories" {
				src = f.cats
			}
			var out []wpTerm
			if slug := q.Get("slug"); slug != "" {
				for id, name := range src {
					if slugify(name) == slug {
						out = append(out, wpTerm{ID: id, Name: name})
					}
				}
				_ = json.NewEncoder(w).Encode(out)
				return
			}
			for _, raw := range splitCommas(q.Get("include")) {
				id, _ := strconv.Atoi(raw)
				if name, ok := src[id]; ok {
					out = append(out, wpTerm{ID: id, Name: name})
				}
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			t.Errorf("unexpected route %q", r.URL.String())
			http.NotFound(w, r)
		}
	}
}

func splitCommas(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	cur := ""
	for _, c := range s {
		if c == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(c)
	}
	return append(out, cur)
}

func slugify(name string) string {
	out := ""
	for _, c := range name {
		switch {
		case c >= 'A' && c <= 'Z':
			out += string(c + 32)
		case c == ' ':
			out += "-"
		default:
			out += string(c)
		}
	}
	return out
}

func scenePost(id int, title, extra string) wpPost {
	p := wpPost{ID: id, Date: "2024-04-26T10:00:00", Slug: "post-" + strconv.Itoa(id)}
	p.Title.Rendered = title
	p.Content.Rendered = `<a href='https://borntobebound.com/updates/wp-content/uploads/2024/04/` +
		strconv.Itoa(id) + `.jpg'><img src="t.jpg"/></a>` +
		`<p><a href="/source/clip.mp4">members right click here to download this mp4</a></p>` +
		`<p>run time 20 minutes</p><p>` + extra + `</p>`
	p.Link = "https://borntobebound.com/updates/?p=" + strconv.Itoa(id)
	return p
}

func newTestScraper(t *testing.T, f *fakeWP) *Scraper {
	t.Helper()
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	return &Scraper{Client: ts.Client(), base: ts.URL + "/updates/"}
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]scraper.SceneResult, []error) {
	t.Helper()
	out := make(chan scraper.SceneResult, 200)
	go s.run(context.Background(), studioURL, opts, out)
	var res []scraper.SceneResult
	var errs []error
	for r := range out {
		if r.Kind == scraper.KindError {
			errs = append(errs, r.Err)
		}
		res = append(res, r)
	}
	return res, errs
}

func scenesOf(res []scraper.SceneResult) []scraper.SceneResult {
	var out []scraper.SceneResult
	for _, r := range res {
		if r.Kind == scraper.KindScene {
			out = append(out, r)
		}
	}
	return out
}

func TestRunFullCatalogue(t *testing.T) {
	f := &fakeWP{
		tags: map[int]string{10: "ballgagged", 11: "high heels"},
		cats: map[int]string{20: "Carissa Dumond", 21: "Chair tie"},
	}
	for i := 1; i <= 5; i++ {
		p := scenePost(100+i, "Scene "+strconv.Itoa(i), "A description.")
		p.Tags = []int{10, 11}
		p.Categories = []int{20, 21}
		f.posts = append(f.posts, p)
	}
	s := newTestScraper(t, f)

	res, errs := collect(t, s, "https://borntobebound.com/updates", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	got := scenesOf(res)
	if len(got) != 5 {
		t.Fatalf("got %d scenes, want 5", len(got))
	}
	sc := got[0].Scene
	if sc.ID != "101" || sc.SiteID != siteID {
		t.Errorf("id/site = %q/%q", sc.ID, sc.SiteID)
	}
	if sc.Title != "Scene 1" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.Duration != 1200 {
		t.Errorf("duration = %d, want 1200", sc.Duration)
	}
	if sc.Description != "A description." {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.Date.Format("2006-01-02") != "2024-04-26" {
		t.Errorf("date = %v", sc.Date)
	}
	if !slices.Equal(sc.Tags, []string{"ballgagged", "high heels"}) {
		t.Errorf("tags = %v", sc.Tags)
	}
	// Categories mix cast and descriptors; both are stored as categories and
	// neither is promoted to a performer.
	if !slices.Equal(sc.Categories, []string{"Carissa Dumond", "Chair tie"}) {
		t.Errorf("categories = %v", sc.Categories)
	}
	if len(sc.Performers) != 0 {
		t.Errorf("performers = %v, want none", sc.Performers)
	}
	// Every scene reuses the same two vocabularies; resolving them a second
	// time would mean the cache is not holding.
	if f.termReqs != 2 {
		t.Errorf("term requests = %d, want 2 (one per vocabulary)", f.termReqs)
	}
}

// A page of nothing but journal entries must not end the walk — roughly one
// post in eight has no video, and early pages of a category can be all journal.
func TestRunSkipsJournalPagesWithoutStopping(t *testing.T) {
	f := &fakeWP{}
	for i := range 100 {
		p := wpPost{ID: 200 + i, Date: "2020-01-01T00:00:00", Slug: "journal"}
		p.Title.Rendered = "Journal Update"
		p.Content.Rendered = "<p>Hello everyone, no video today.</p>"
		f.posts = append(f.posts, p)
	}
	f.posts = append(f.posts, scenePost(999, "Real scene", "Prose."))
	s := newTestScraper(t, f)

	res, errs := collect(t, s, "https://borntobebound.com/updates", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	got := scenesOf(res)
	if len(got) != 1 || got[0].Scene.ID != "999" {
		t.Fatalf("got %d scenes, want the one behind the journal page", len(got))
	}
	if !slices.Equal(f.pageHits, []int{1, 2}) {
		t.Errorf("pages fetched = %v, want both", f.pageHits)
	}
}

func TestRunTagFilterResolvesSlug(t *testing.T) {
	f := &fakeWP{tags: map[int]string{77: "Armbinder"}}
	p := scenePost(300, "Tagged scene", "Prose.")
	p.Tags = []int{77}
	f.posts = append(f.posts, p)
	s := newTestScraper(t, f)

	res, errs := collect(t, s, "https://borntobebound.com/updates/?tag=armbinder", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if got := scenesOf(res); len(got) != 1 {
		t.Fatalf("got %d scenes, want 1", len(got))
	}
}

func TestRunUnknownTagIsAbsentNotIncomplete(t *testing.T) {
	f := &fakeWP{tags: map[int]string{}}
	s := newTestScraper(t, f)

	_, errs := collect(t, s, "https://borntobebound.com/updates/?tag=nosuchtag", scraper.ListOpts{})
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureAbsent {
		t.Errorf("classified as %v, want FailureAbsent", k)
	}
}

func TestResolveFilterCategory(t *testing.T) {
	s := New()
	f, err := s.resolveFilter(context.Background(), "https://borntobebound.com/updates/?cat=116")
	if err != nil {
		t.Fatal(err)
	}
	if f.param != "categories" || f.value != "116" {
		t.Errorf("filter = %+v", f)
	}

	f, err = s.resolveFilter(context.Background(), "https://borntobebound.com/updates")
	if err != nil {
		t.Fatal(err)
	}
	if f.param != "" {
		t.Errorf("filter = %+v, want none", f)
	}
}

func TestRestURLKeepsRouteAndParams(t *testing.T) {
	s := New()
	q := url.Values{}
	q.Set("per_page", "100")
	u, err := url.Parse(s.restURL("/wp/v2/posts", q))
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("rest_route"); got != "/wp/v2/posts" {
		t.Errorf("rest_route = %q", got)
	}
	if got := u.Query().Get("per_page"); got != "100" {
		t.Errorf("per_page = %q", got)
	}
}
