package dorcelclub

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

var _ scraper.StudioScraper = (*Scraper)(nil)

// ---- TestMatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.dorcelclub.com", true},
		{"https://dorcelclub.com/en/pornstar/anna-polina", true},
		{"https://www.dorcelclub.com/en/collection/dorcel-airlines", true},
		{"https://www.dorcelclub.com/en/fantasmes/threesome", true},
		{"https://www.dorcelclub.com/en/porn-movie/luxure-the-education", true},
		{"http://dorcelclub.com/en/scene/123/some-scene", true},
		{"https://www.example.com", false},
		{"https://dorcel.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// ---- TestStripTags ----

func TestStripTags(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"plain text", "plain text"},
		{"<b>bold</b> text", "bold  text"},
		{"<p>paragraph</p>", "paragraph"},
		{"<a href=\"/foo\">link</a> and <em>emphasis</em>", "link  and  emphasis"},
		{"  spaces  around  ", "spaces  around"},
		{"", ""},
		{"no tags at all", "no tags at all"},
		{"<br/>line<br>break", "line break"},
		{"nested <div><span>content</span></div> here", "nested   content   here"},
	}
	for _, c := range cases {
		got := stripTags(c.input)
		if got != c.want {
			t.Errorf("stripTags(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// ---- fixtures ----

func sceneCardHTML(id, slug, title, thumb string, performers []string) string {
	var actorLinks strings.Builder
	for _, p := range performers {
		fmt.Fprintf(&actorLinks, `<a href="/en/pornstar/%s">%s</a>`, strings.ReplaceAll(strings.ToLower(p), " ", "-"), p)
	}

	var actorsDiv string
	if len(performers) > 0 {
		actorsDiv = fmt.Sprintf(`<div class="actors">%s</div>`, actorLinks.String())
	}

	return fmt.Sprintf(`<div class="scene thumbnail active">
<a href="/en/scene/%s/%s" class="thumb">
<img class="lazy" data-src="%s" alt="%s">
</a>
<a href="/en/scene/%s/%s" class="title">
  %s
</a>
%s
</div>
</div>`, id, slug, thumb, title, id, slug, title, actorsDiv)
}

func listingPage(cards []string, hasMore bool) string {
	var sb strings.Builder
	sb.WriteString("<html><body>")
	for _, card := range cards {
		fmt.Fprintf(&sb, "%s\n", card)
	}
	if hasMore {
		sb.WriteString(`<a class="btn-more" href="#">Load More</a>`)
	}
	sb.WriteString("</body></html>")
	return sb.String()
}

func detailPage(date, mins, secs, desc, movieSlug, movieName, director string, performers []string) string {
	var sb strings.Builder
	sb.WriteString("<html><body>")
	if date != "" {
		fmt.Fprintf(&sb, `<span class="publish_date">%s</span>`, date)
	}
	if mins != "" {
		fmt.Fprintf(&sb, `<span class="duration">%sm%s</span>`, mins, secs)
	}
	if desc != "" {
		fmt.Fprintf(&sb, `<span class="full">%s</span>`, desc)
	}
	if movieSlug != "" {
		fmt.Fprintf(&sb, `<span class="movie"><a href="/en/porn-movie/%s">%s</a></span>`, movieSlug, movieName)
	}
	if director != "" {
		fmt.Fprintf(&sb, `<span class="director">Director: %s</span>`, director)
	}
	if len(performers) > 0 {
		sb.WriteString(`<div class="actress">`)
		for _, p := range performers {
			fmt.Fprintf(&sb, `<a href="/en/pornstar/%s">%s</a>`, strings.ReplaceAll(strings.ToLower(p), " ", "-"), p)
		}
		sb.WriteString(`</div>`)
	}
	sb.WriteString("</body></html>")
	return sb.String()
}

// ---- TestParseSceneCards ----

func TestParseSceneCards(t *testing.T) {
	card1 := sceneCardHTML("1001", "hot-scene", "Hot Scene", "https://cdn.example.com/thumb1.jpg", []string{"Anna Polina", "Claire Castel"})
	card2 := sceneCardHTML("1002", "another-scene", "Another Scene", "https://cdn.example.com/thumb2.jpg", []string{"Lana Rhoades"})
	page := listingPage([]string{card1, card2}, false)

	items := parseSceneCards(page)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	s := items[0]
	if s.id != "1001" {
		t.Errorf("id = %q, want %q", s.id, "1001")
	}
	if s.url != siteBase+"/en/scene/1001/hot-scene" {
		t.Errorf("url = %q, want %q", s.url, siteBase+"/en/scene/1001/hot-scene")
	}
	if s.title != "Hot Scene" {
		t.Errorf("title = %q, want %q", s.title, "Hot Scene")
	}
	if s.thumb != "https://cdn.example.com/thumb1.jpg" {
		t.Errorf("thumb = %q, want %q", s.thumb, "https://cdn.example.com/thumb1.jpg")
	}
	if len(s.performers) != 2 {
		t.Fatalf("performers count = %d, want 2", len(s.performers))
	}
	if s.performers[0] != "Anna Polina" {
		t.Errorf("performers[0] = %q, want %q", s.performers[0], "Anna Polina")
	}
	if s.performers[1] != "Claire Castel" {
		t.Errorf("performers[1] = %q, want %q", s.performers[1], "Claire Castel")
	}

	s2 := items[1]
	if s2.id != "1002" {
		t.Errorf("id = %q, want %q", s2.id, "1002")
	}
	if s2.title != "Another Scene" {
		t.Errorf("title = %q, want %q", s2.title, "Another Scene")
	}
	if len(s2.performers) != 1 || s2.performers[0] != "Lana Rhoades" {
		t.Errorf("performers = %v, want [Lana Rhoades]", s2.performers)
	}
}

func TestParseSceneCardsHTMLEntities(t *testing.T) {
	card := sceneCardHTML("2001", "scene-amp", "Love &amp; Desire", "https://cdn.example.com/thumb.jpg", nil)
	page := listingPage([]string{card}, false)

	items := parseSceneCards(page)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].title != "Love & Desire" {
		t.Errorf("title = %q, want %q", items[0].title, "Love & Desire")
	}
}

func TestParseSceneCardsDedup(t *testing.T) {
	card := sceneCardHTML("3001", "dup-scene", "Dup Scene", "https://cdn.example.com/thumb.jpg", []string{"Performer"})
	page := listingPage([]string{card, card}, false)

	items := parseSceneCards(page)
	if len(items) != 1 {
		t.Errorf("got %d items, want 1 (dedup)", len(items))
	}
}

func TestParseSceneCardsEmpty(t *testing.T) {
	page := listingPage(nil, false)
	items := parseSceneCards(page)
	if len(items) != 0 {
		t.Errorf("got %d items, want 0", len(items))
	}
}

func TestParseSceneCardsNoPerformers(t *testing.T) {
	card := sceneCardHTML("4001", "solo-scene", "Solo Scene", "https://cdn.example.com/thumb.jpg", nil)
	page := listingPage([]string{card}, false)

	items := parseSceneCards(page)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if len(items[0].performers) != 0 {
		t.Errorf("performers = %v, want empty", items[0].performers)
	}
}

func TestParseSceneCardsNoThumb(t *testing.T) {
	// Card without data-src attribute on img
	card := `<div class="scene thumbnail active">
<a href="/en/scene/5001/no-thumb" class="thumb">
<img class="lazy" alt="No Thumb">
</a>
<a href="/en/scene/5001/no-thumb" class="title">No Thumb Scene</a>
</div>
</div>`
	page := listingPage([]string{card}, false)

	items := parseSceneCards(page)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].thumb != "" {
		t.Errorf("thumb = %q, want empty", items[0].thumb)
	}
}

func TestBtnMoreDetection(t *testing.T) {
	card := sceneCardHTML("6001", "test", "Test", "https://cdn.example.com/t.jpg", nil)

	withMore := listingPage([]string{card}, true)
	if !btnMoreRe.MatchString(withMore) {
		t.Error("expected btn-more match in page with more button")
	}

	withoutMore := listingPage([]string{card}, false)
	if btnMoreRe.MatchString(withoutMore) {
		t.Error("expected no btn-more match in page without more button")
	}
}

// ---- Detail page regex tests ----

func TestDetailDateRegex(t *testing.T) {
	body := detailPage("January 02, 2026", "", "", "", "", "", "", nil)
	m := detailDateRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("detailDateRe did not match")
	}
	if strings.TrimSpace(m[1]) != "January 02, 2026" {
		t.Errorf("date = %q, want %q", m[1], "January 02, 2026")
	}
}

func TestDetailDurationRegex(t *testing.T) {
	body := detailPage("", "30", "45", "", "", "", "", nil)
	m := detailDurationRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("detailDurationRe did not match")
	}
	if m[1] != "30" || m[2] != "45" {
		t.Errorf("duration = %sm%s, want 30m45", m[1], m[2])
	}
}

func TestDetailDescRegex(t *testing.T) {
	body := detailPage("", "", "", "A steamy encounter in Paris.", "", "", "", nil)
	m := detailDescRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("detailDescRe did not match")
	}
	if strings.TrimSpace(m[1]) != "A steamy encounter in Paris." {
		t.Errorf("desc = %q", m[1])
	}
}

func TestDetailDescWithHTMLTags(t *testing.T) {
	body := detailPage("", "", "", "<b>Bold</b> description with <a href=\"#\">link</a>.", "", "", "", nil)
	m := detailDescRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("detailDescRe did not match")
	}
	got := stripTags(strings.TrimSpace(m[1]))
	if got != "Bold  description with  link ." {
		t.Errorf("stripped desc = %q", got)
	}
}

func TestDetailMovieRegex(t *testing.T) {
	body := detailPage("", "", "", "", "luxure-the-education", "Luxure The Education", "", nil)
	m := detailMovieRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("detailMovieRe did not match")
	}
	if strings.TrimSpace(m[1]) != "Luxure The Education" {
		t.Errorf("movie = %q, want %q", m[1], "Luxure The Education")
	}
}

func TestDetailDirectorRegex(t *testing.T) {
	body := detailPage("", "", "", "", "", "", "Herve Bodilis", nil)
	m := detailDirectorRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("detailDirectorRe did not match")
	}
	if strings.TrimSpace(m[1]) != "Herve Bodilis" {
		t.Errorf("director = %q, want %q", m[1], "Herve Bodilis")
	}
}

func TestDetailActressRegex(t *testing.T) {
	body := detailPage("", "", "", "", "", "", "", []string{"Anna Polina", "Claire Castel"})
	m := detailActressRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("detailActressRe did not match")
	}
	var performers []string
	for _, pm := range actorRe.FindAllStringSubmatch(m[1], -1) {
		performers = append(performers, strings.TrimSpace(pm[1]))
	}
	if len(performers) != 2 {
		t.Fatalf("performers count = %d, want 2", len(performers))
	}
	if performers[0] != "Anna Polina" || performers[1] != "Claire Castel" {
		t.Errorf("performers = %v", performers)
	}
}

// ---- redirectTransport ----

// redirectTransport rewrites requests targeting siteBase to the test server.
type redirectTransport struct {
	base      http.RoundTripper
	tsURL     string
	targetURL string
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.String(), rt.targetURL) {
		u, _ := url.Parse(rt.tsURL + req.URL.Path)
		u.RawQuery = req.URL.RawQuery
		req = req.Clone(req.Context())
		req.URL = u
	}
	return rt.base.RoundTrip(req)
}

// ---- TestRunPornstar (httptest) ----

func TestRunPornstar(t *testing.T) {
	card1 := sceneCardHTML("101", "scene-one", "Scene One", "https://cdn.example.com/t1.jpg", []string{"Performer A"})
	card2 := sceneCardHTML("102", "scene-two", "Scene Two", "https://cdn.example.com/t2.jpg", []string{"Performer B", "Performer C"})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/en/":
			w.WriteHeader(http.StatusOK)
		case "/en/pornstar/performer-a":
			_, _ = fmt.Fprint(w, listingPage([]string{card1, card2}, false))
		case "/en/scene/101/scene-one":
			_, _ = fmt.Fprint(w, detailPage("March 15, 2026", "25", "30", "First scene description.", "luxure", "Luxure", "John Director", []string{"Performer A"}))
		case "/en/scene/102/scene-two":
			_, _ = fmt.Fprint(w, detailPage("February 10, 2026", "40", "00", "Second scene description.", "", "", "", []string{"Performer B", "Performer C"}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	c := ts.Client()
	c.Jar = jar
	c.Transport = &redirectTransport{
		base:      c.Transport,
		tsURL:     ts.URL,
		targetURL: siteBase,
	}
	s := &Scraper{client: c, base: ts.URL}

	studioURL := ts.URL + "/en/pornstar/performer-a"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{Workers: 2, Delay: 0})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	byID := map[string]struct {
		title       string
		desc        string
		duration    int
		series      string
		director    string
		performers  []string
		studioField string
	}{}
	for _, sc := range scenes {
		byID[sc.ID] = struct {
			title       string
			desc        string
			duration    int
			series      string
			director    string
			performers  []string
			studioField string
		}{
			title:       sc.Title,
			desc:        sc.Description,
			duration:    sc.Duration,
			series:      sc.Series,
			director:    sc.Director,
			performers:  sc.Performers,
			studioField: sc.Studio,
		}
	}

	s1, ok := byID["101"]
	if !ok {
		t.Fatal("scene 101 not found")
	}
	if s1.title != "Scene One" {
		t.Errorf("scene 101 title = %q, want %q", s1.title, "Scene One")
	}
	if s1.desc != "First scene description." {
		t.Errorf("scene 101 description = %q, want %q", s1.desc, "First scene description.")
	}
	if s1.duration != 25*60+30 {
		t.Errorf("scene 101 duration = %d, want %d", s1.duration, 25*60+30)
	}
	if s1.series != "Luxure" {
		t.Errorf("scene 101 series = %q, want %q", s1.series, "Luxure")
	}
	if s1.director != "John Director" {
		t.Errorf("scene 101 director = %q, want %q", s1.director, "John Director")
	}
	// Detail page overrides listing performers
	if len(s1.performers) != 1 || s1.performers[0] != "Performer A" {
		t.Errorf("scene 101 performers = %v, want [Performer A]", s1.performers)
	}
	if s1.studioField != studioName {
		t.Errorf("scene 101 studio = %q, want %q", s1.studioField, studioName)
	}

	s2, ok := byID["102"]
	if !ok {
		t.Fatal("scene 102 not found")
	}
	if s2.title != "Scene Two" {
		t.Errorf("scene 102 title = %q, want %q", s2.title, "Scene Two")
	}
	if s2.duration != 40*60 {
		t.Errorf("scene 102 duration = %d, want %d", s2.duration, 40*60)
	}
	if len(s2.performers) != 2 || s2.performers[0] != "Performer B" || s2.performers[1] != "Performer C" {
		t.Errorf("scene 102 performers = %v, want [Performer B Performer C]", s2.performers)
	}
}

// ---- TestRunPornstarKnownIDs ----

func TestRunPornstarKnownIDs(t *testing.T) {
	card1 := sceneCardHTML("201", "first-scene", "First Scene", "https://cdn.example.com/t1.jpg", []string{"Alice"})
	card2 := sceneCardHTML("202", "second-scene", "Second Scene", "https://cdn.example.com/t2.jpg", []string{"Bob"})
	card3 := sceneCardHTML("203", "third-scene", "Third Scene", "https://cdn.example.com/t3.jpg", []string{"Charlie"})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/en/":
			w.WriteHeader(http.StatusOK)
		case "/en/pornstar/alice":
			_, _ = fmt.Fprint(w, listingPage([]string{card1, card2, card3}, false))
		case "/en/scene/201/first-scene":
			_, _ = fmt.Fprint(w, detailPage("January 01, 2026", "10", "00", "First.", "", "", "", nil))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	c := ts.Client()
	c.Jar = jar
	c.Transport = &redirectTransport{
		base:      c.Transport,
		tsURL:     ts.URL,
		targetURL: siteBase,
	}
	s := &Scraper{client: c, base: ts.URL}

	studioURL := ts.URL + "/en/pornstar/alice"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{
		Workers:  1,
		Delay:    0,
		KnownIDs: map[string]bool{"202": true},
	})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if scenes[0].ID != "201" {
		t.Errorf("scene ID = %q, want %q", scenes[0].ID, "201")
	}
}

// French pages carry no locale prefix and every other language carries its own,
// and the slug after the id is localised too — so the detail URL has to come
// from the card rather than being rebuilt as /en/.
func TestParseSceneCardsKeepsCardLocale(t *testing.T) {
	cases := []struct{ href, wantURL string }{
		{"/de/scene/4001/heisse-szene", siteBase + "/de/scene/4001/heisse-szene"},
		{"/scene/4001/scene-chaude", siteBase + "/scene/4001/scene-chaude"},
	}
	for _, c := range cases {
		card := fmt.Sprintf(`<div class="scene thumbnail active">
<a href="%s" class="thumb">
<img class="lazy" data-src="https://cdn.example.com/t.jpg" alt="x">
</a>
<a href="%s" class="title">
  Titel
</a>
</div>
</div>`, c.href, c.href)
		items := parseSceneCards(listingPage([]string{card}, false))
		if len(items) != 1 {
			t.Fatalf("%s: got %d items, want 1", c.href, len(items))
		}
		if items[0].id != "4001" {
			t.Errorf("%s: id = %q, want 4001", c.href, items[0].id)
		}
		if items[0].url != c.wantURL {
			t.Errorf("%s: url = %q, want %q", c.href, items[0].url, c.wantURL)
		}
		if items[0].title != "Titel" {
			t.Errorf("%s: title = %q, want Titel", c.href, items[0].title)
		}
	}
}

// The site redirects a locale path whose language the Accept-Language header
// contradicts, so the header has to state the requested language or the whole
// run silently comes back in English.
func TestPageHeadersStateTheLanguage(t *testing.T) {
	if got := pageHeaders("de")["Accept-Language"]; got != "de,en;q=0.5" {
		t.Errorf("Accept-Language = %q, want %q", got, "de,en;q=0.5")
	}
	if got := pageHeaders("")["Accept-Language"]; got != "en-US,en;q=0.5" {
		t.Errorf("default Accept-Language = %q, want the browser default", got)
	}
	if pageHeaders("fr")["Accept-Language"] != "fr,en;q=0.5" {
		t.Error("fr must state itself in the header")
	}
	if ajaxHeaders("de")["X-Requested-With"] != "XMLHttpRequest" {
		t.Error("ajaxHeaders must keep the XHR marker")
	}
}

func TestSessionBootstrapFollowsLanguage(t *testing.T) {
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path+"|"+r.Header.Get("Accept-Language"))
		_, _ = fmt.Fprint(w, "<html></html>")
	}))
	defer ts.Close()

	s := New()
	s.client = ts.Client()
	s.base = ts.URL
	if err := s.initSession(context.Background(), "de"); err != nil {
		t.Fatalf("initSession: %v", err)
	}
	if len(got) != 1 || got[0] != "/de/|de,en;q=0.5" {
		t.Errorf("bootstrap = %v, want /de/ with a German Accept-Language", got)
	}
}

func TestLanguagesAdvertisesEnglishFirst(t *testing.T) {
	langs := New().Languages()
	if len(langs) == 0 || langs[0] != "en" {
		t.Fatalf("Languages() = %v, want en first", langs)
	}
	for _, l := range langs {
		if tag, err := scraper.NormalizeLanguage(l); err != nil || tag != l {
			t.Errorf("Languages() entry %q is not a normalized tag", l)
		}
	}
}
