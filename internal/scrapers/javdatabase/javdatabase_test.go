package javdatabase

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

// ---- fixtures ----

type cardFixture struct {
	url        string
	label      string
	thumb      string
	title      string
	date       string
	studioSlug string
	studioName string
}

func listingHTML(cards []cardFixture, lastPage int) []byte {
	var sb strings.Builder
	sb.WriteString(`<html><head>`)
	sb.WriteString(`<meta name="twitter:data1" content="42" />`)
	sb.WriteString(`</head><body><div class="row">`)
	for _, card := range cards {
		fmt.Fprintf(&sb, `
<div class="col-md-3 col-lg-2 col-xxl-2 col-4">
  <div class="card h-100 borderlesscard">
    <div class="card-body d-flex flex-column">
      <p class="display-6 pcard">
        <a href="%s" class="cut-text">%s</a>
      </p>
      <div class="movie-cover-thumb">
        <a href="%s"><img src="%s" width="147" height="200"></a>
      </div>
      <div class="mt-auto" style="text-align: center;">
        <a href="%s" class="cut-text">%s</a>
        %s
      </div>
      <span class="btn btn-primary btn-sm cut-text">
        <a href="/studios/%s/" rel="tag">%s</a>
      </span>
    </div>
  </div>
</div>`, card.url, card.label, card.url, card.thumb, card.url, card.title, card.date, card.studioSlug, card.studioName)
	}
	if lastPage > 1 {
		fmt.Fprintf(&sb, `
<nav><ul class="pagination">
  <li class="page-item"><a class="page-link" href="/studios/test/page/%d/" aria-label="Last Page">&raquo;</a></li>
</ul></nav>`, lastPage)
	}
	sb.WriteString(`</div></body></html>`)
	return []byte(sb.String())
}

func sponsoredCardHTML() string {
	return `
<div class="col-md-3 col-lg-2 col-xxl-2 col-4">
  <div class="card h-100 borderlesscard">
    <div class="card-body d-flex flex-column">
      <p class="display-6 pcard">
        <a href="https://www.spermmania.com/en/" class="cut-text" target="_blank"
           data-source="school-bukkake" rel="sponsored">School Bukkake</a>
      </p>
      <div class="movie-cover-thumb">
        <a href="https://www.spermmania.com/en/" target="_blank" rel="sponsored">
          <img src="/vertical/spermmania/sperm03.jpg" width="147" height="200">
        </a>
      </div>
      <div class="mt-auto" style="text-align: center;">2026-05-15</div>
      <span class="btn btn-primary btn-sm cut-text">
        <a href="https://www.spermmania.com/en/" rel="tag" target="_blank">Sperm Mania</a>
      </span>
    </div>
  </div>
</div>`
}

func censoredDetailHTML(title, dvdID, date, runtime, studio, director, series string, genres, performers []string) []byte {
	var sb strings.Builder
	sb.WriteString(`<html><body>`)
	fmt.Fprintf(&sb, `<p class="mb-1"><b>Title: </b>%s</p>`, title)
	fmt.Fprintf(&sb, `<p class="mb-1"><b>DVD ID: </b>%s</p>`, dvdID)
	fmt.Fprintf(&sb, `<p class="mb-1"><b>Release Date: </b>%s</p>`, date)
	fmt.Fprintf(&sb, `<p class="mb-1"><b>Runtime: </b>%s</p>`, runtime)
	fmt.Fprintf(&sb, `<p class="mb-1"><b>Studio: </b><span class="btn btn-primary btn-sm"><a href="/studios/test/">%s</a></span></p>`, studio)
	fmt.Fprintf(&sb, `<p class="mb-1"><b>Director: </b>%s</p>`, director)

	if series != "" {
		fmt.Fprintf(&sb, `<p class="mb-1"><b>JAV Series: </b><span class="btn btn-primary btn-sm"><a href="/series/test/">%s</a></span></p>`, series)
	} else {
		sb.WriteString(`<p class="mb-1"><b>JAV Series: </b></p>`)
	}

	sb.WriteString(`<p class="mb-1"><b>Genre(s): </b>`)
	for _, g := range genres {
		fmt.Fprintf(&sb, `<span class="btn btn-primary btn-sm"><a href="/genres/test/">%s</a></span> `, g)
	}
	sb.WriteString(`</p>`)

	sb.WriteString(`<p class="mb-1"><b>Idol(s)/Actress(es): </b>`)
	for _, p := range performers {
		fmt.Fprintf(&sb, `<span class="btn btn-primary btn-sm"><a href="/idols/test/">%s</a></span> `, p)
	}
	sb.WriteString(`</p>`)

	sb.WriteString(`</body></html>`)
	return []byte(sb.String())
}

func uncensoredDetailHTML(title, date, runtime, studio, series string, genres, performers []string) []byte {
	var sb strings.Builder
	sb.WriteString(`<html><body>`)
	sb.WriteString(`<table class="table table-striped table-hover">`)
	fmt.Fprintf(&sb, `<tr><td class="tablelabel"><b>Title:</b></td><td class="tablevalue" colspan="2">%s</td></tr>`, title)
	fmt.Fprintf(&sb, `<tr><td class="tablelabel"><b>Release Date:</b></td><td class="tablevalue" colspan="2">%s</td></tr>`, date)
	fmt.Fprintf(&sb, `<tr><td class="tablelabel"><b>Runtime:</b></td><td class="tablevalue" colspan="2">%s</td></tr>`, runtime)

	sb.WriteString(`<tr><td class="tablelabel"><b>Genre(s): </b></td><td class="tablevalue" colspan="2">`)
	for _, g := range genres {
		fmt.Fprintf(&sb, `<span class="btn btn-primary btn-sm"><a href="/genres/test/">%s</a></span> `, g)
	}
	sb.WriteString(`</td></tr>`)

	fmt.Fprintf(&sb, `<tr><td class="tablelabel"><b>Studio: </b></td><td class="tablevalue" colspan="2"><span class="btn btn-primary btn-sm"><a href="/studios/test/">%s</a></span></td></tr>`, studio)

	if series != "" {
		fmt.Fprintf(&sb, `<tr><td class="tablelabel"><b>Series: </b></td><td class="tablevalue" colspan="2"><span class="btn btn-primary btn-sm"><a href="/series/test/">%s</a></span></td></tr>`, series)
	}

	sb.WriteString(`</table>`)

	sb.WriteString(`<h4 class="subhead">Actress/Idols</h4><div class="row">`)
	for _, p := range performers {
		fmt.Fprintf(&sb, `<div class="col-md-3"><p class="display-6"><a class="cut-text" href="/idols/%s/">%s</a></p></div>`, strings.ReplaceAll(strings.ToLower(p), " ", "-"), p)
	}
	sb.WriteString(`</div>`)

	sb.WriteString(`</body></html>`)
	return []byte(sb.String())
}

func testCard1() cardFixture {
	return cardFixture{
		url:        "https://www.javdatabase.com/movies/test-001/",
		label:      "TEST-001",
		thumb:      "https://www.javdatabase.com/covers/thumb/t/test001ps.webp",
		title:      "Test Movie One",
		date:       "2026-01-15",
		studioSlug: "test-studio",
		studioName: "Test Studio",
	}
}

func testCard2() cardFixture {
	return cardFixture{
		url:        "https://www.javdatabase.com/movies/test-002/",
		label:      "TEST-002",
		thumb:      "https://www.javdatabase.com/covers/thumb/t/test002ps.webp",
		title:      "Test Movie Two &amp; More",
		date:       "2026-01-10",
		studioSlug: "test-studio",
		studioName: "Test Studio",
	}
}

func testUncensoredCard() cardFixture {
	return cardFixture{
		url:        "https://www.javdatabase.com/uncensored/wild-night-caribbeancom-2026-01-05/",
		label:      "Wild Night",
		thumb:      "https://www.javdatabase.com/covers/thumb/u/uncen001ps.webp",
		title:      "Wild Night",
		date:       "2026-01-05",
		studioSlug: "caribbeancom",
		studioName: "Caribbeancom",
	}
}

// ---- TestMatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.javdatabase.com/studios/moodyz/", true},
		{"https://javdatabase.com/studios/moodyz/", true},
		{"https://www.javdatabase.com/movies/", true},
		{"https://www.javdatabase.com/uncensored/", true},
		{"https://www.javdatabase.com/genres/embarrassment/", true},
		{"https://www.javdatabase.com/series/sod-female-employee/", true},
		{"https://www.javdatabase.com/idols/erika-komura/", true},
		{"https://www.javdatabase.com/movies/dass-948/", true},
		{"https://www.javdatabase.com/", false},
		{"https://www.pornhub.com/channels/test", false},
		{"", false},
	}
	for _, c := range cases {
		got := s.MatchesURL(c.url)
		if got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// ---- TestBuildPageURL ----

func TestBuildPageURL(t *testing.T) {
	cases := []struct {
		input string
		page  int
		want  string
	}{
		{"https://www.javdatabase.com/studios/moodyz/", 1, "https://www.javdatabase.com/studios/moodyz/"},
		{"https://www.javdatabase.com/studios/moodyz/", 2, "https://www.javdatabase.com/studios/moodyz/page/2/"},
		{"https://www.javdatabase.com/studios/moodyz", 3, "https://www.javdatabase.com/studios/moodyz/page/3/"},
		{"https://www.javdatabase.com/movies/", 1, "https://www.javdatabase.com/movies/"},
		{"https://www.javdatabase.com/movies/", 5, "https://www.javdatabase.com/movies/page/5/"},
	}
	for _, c := range cases {
		got := buildPageURL(c.input, c.page)
		if got != c.want {
			t.Errorf("buildPageURL(%q, %d) = %q, want %q", c.input, c.page, got, c.want)
		}
	}
}

// ---- TestExtractSlug ----

func TestExtractSlug(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.javdatabase.com/movies/dass-948/", "dass-948"},
		{"https://www.javdatabase.com/uncensored/wild-night-caribbeancom-2026/", "wild-night-caribbeancom-2026"},
		{"https://www.javdatabase.com/studios/moodyz/", ""},
	}
	for _, c := range cases {
		got := extractSlug(c.url)
		if got != c.want {
			t.Errorf("extractSlug(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// ---- TestParseListingPage ----

func TestParseListingPage(t *testing.T) {
	body := listingHTML([]cardFixture{testCard1(), testCard2()}, 0)
	items := parseListingPage(body)

	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	got := items[0]
	if got.id != "TEST-001" {
		t.Errorf("id = %q, want TEST-001", got.id)
	}
	if got.title != "Test Movie One" {
		t.Errorf("title = %q, want %q", got.title, "Test Movie One")
	}
	if got.date != "2026-01-15" {
		t.Errorf("date = %q, want 2026-01-15", got.date)
	}
	if got.thumb != testCard1().thumb {
		t.Errorf("thumb = %q, want %q", got.thumb, testCard1().thumb)
	}
	if got.studio != "Test Studio" {
		t.Errorf("studio = %q, want %q", got.studio, "Test Studio")
	}
	if got.uncensored {
		t.Error("expected censored, got uncensored")
	}
}

func TestParseListingPageHTMLEntities(t *testing.T) {
	body := listingHTML([]cardFixture{testCard2()}, 0)
	items := parseListingPage(body)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].title != "Test Movie Two & More" {
		t.Errorf("title = %q, want %q", items[0].title, "Test Movie Two & More")
	}
}

func TestParseListingPageUncensored(t *testing.T) {
	body := listingHTML([]cardFixture{testUncensoredCard()}, 0)
	items := parseListingPage(body)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	got := items[0]
	if got.id != "wild-night-caribbeancom-2026-01-05" {
		t.Errorf("id = %q, want slug-based ID", got.id)
	}
	if !got.uncensored {
		t.Error("expected uncensored")
	}
}

func TestParseListingPageFiltersSponsored(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<html><body>`)
	sb.WriteString(sponsoredCardHTML())

	card := testCard1()
	fmt.Fprintf(&sb, `
<p class="display-6 pcard">
  <a href="%s" class="cut-text">%s</a>
</p>
<div class="movie-cover-thumb"><a href="%s"><img src="%s" width="147" height="200"></a></div>
<div class="mt-auto" style="text-align: center;"><a href="%s" class="cut-text">%s</a> %s</div>
<span class="btn btn-primary btn-sm cut-text"><a href="/studios/%s/" rel="tag">%s</a></span>`,
		card.url, card.label, card.url, card.thumb, card.url, card.title, card.date, card.studioSlug, card.studioName)

	sb.WriteString(`</body></html>`)

	items := parseListingPage([]byte(sb.String()))
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (sponsored should be filtered)", len(items))
	}
	if items[0].id != "TEST-001" {
		t.Errorf("id = %q, want TEST-001", items[0].id)
	}
}

// ---- TestParseLastPage ----

func TestParseLastPage(t *testing.T) {
	body := listingHTML([]cardFixture{testCard1()}, 579)
	got := parseLastPage(body)
	if got != 579 {
		t.Errorf("parseLastPage = %d, want 579", got)
	}
}

func TestParseLastPageMissing(t *testing.T) {
	body := listingHTML([]cardFixture{testCard1()}, 0)
	got := parseLastPage(body)
	if got != 0 {
		t.Errorf("parseLastPage = %d, want 0", got)
	}
}

// ---- TestParseTotal ----

func TestParseTotal(t *testing.T) {
	body := listingHTML([]cardFixture{testCard1()}, 0)
	got := parseTotal(body)
	if got != 42 {
		t.Errorf("parseTotal = %d, want 42", got)
	}
}

// ---- TestParseCensoredDetail ----

func TestParseCensoredDetail(t *testing.T) {
	body := censoredDetailHTML(
		"Sexual Mother&#39;s Conception",
		"DASS-948",
		"2026-05-22",
		"140 min.",
		"Das",
		"John Doe",
		"Test Series",
		[]string{"Big Tits", "Blow Job"},
		[]string{"Hana Himesaki", "Yui Hatano"},
	)
	info := parseCensoredDetail(body)

	if info.title != "Sexual Mother's Conception" {
		t.Errorf("title = %q", info.title)
	}
	if info.date != "2026-05-22" {
		t.Errorf("date = %q", info.date)
	}
	if info.duration != 8400 {
		t.Errorf("duration = %d, want 8400 (140*60)", info.duration)
	}
	if info.studio != "Das" {
		t.Errorf("studio = %q", info.studio)
	}
	if info.director != "John Doe" {
		t.Errorf("director = %q", info.director)
	}
	if info.series != "Test Series" {
		t.Errorf("series = %q", info.series)
	}
	if len(info.tags) != 2 || info.tags[0] != "Big Tits" || info.tags[1] != "Blow Job" {
		t.Errorf("tags = %v", info.tags)
	}
	if len(info.performers) != 2 || info.performers[0] != "Hana Himesaki" || info.performers[1] != "Yui Hatano" {
		t.Errorf("performers = %v", info.performers)
	}
}

func TestParseCensoredDetailEmptyFields(t *testing.T) {
	body := censoredDetailHTML("Title Only", "TEST-001", "2026-01-01", "", "", "", "", nil, nil)
	info := parseCensoredDetail(body)

	if info.title != "Title Only" {
		t.Errorf("title = %q", info.title)
	}
	if info.duration != 0 {
		t.Errorf("duration = %d, want 0", info.duration)
	}
	if info.director != "" {
		t.Errorf("director = %q, want empty", info.director)
	}
	if info.series != "" {
		t.Errorf("series = %q, want empty", info.series)
	}
}

// ---- TestParseUncensoredDetail ----

func TestParseUncensoredDetail(t *testing.T) {
	body := uncensoredDetailHTML(
		"Wild Night in Tokyo",
		"2026-05-13",
		"58 min.",
		"Caribbeancom",
		"Caribbean Series",
		[]string{"Creampie", "Threesome"},
		[]string{"Mina Sakura", "Yui Hatano"},
	)
	info := parseUncensoredDetail(body)

	if info.title != "Wild Night in Tokyo" {
		t.Errorf("title = %q", info.title)
	}
	if info.date != "2026-05-13" {
		t.Errorf("date = %q", info.date)
	}
	if info.duration != 3480 {
		t.Errorf("duration = %d, want 3480 (58*60)", info.duration)
	}
	if info.studio != "Caribbeancom" {
		t.Errorf("studio = %q", info.studio)
	}
	if info.series != "Caribbean Series" {
		t.Errorf("series = %q", info.series)
	}
	if len(info.tags) != 2 || info.tags[0] != "Creampie" || info.tags[1] != "Threesome" {
		t.Errorf("tags = %v", info.tags)
	}
	if len(info.performers) != 2 || info.performers[0] != "Mina Sakura" || info.performers[1] != "Yui Hatano" {
		t.Errorf("performers = %v", info.performers)
	}
}

// ---- TestListScenes ----

func TestListScenes(t *testing.T) {
	detailBodies := map[string][]byte{
		"/movies/test-001/": censoredDetailHTML("Full Title One", "TEST-001", "2026-01-15", "120 min.", "Test Studio", "", "", []string{"Tag A"}, []string{"Actor One"}),
		"/movies/test-002/": censoredDetailHTML("Full Title Two", "TEST-002", "2026-01-10", "90 min.", "Test Studio", "", "", []string{"Tag B"}, []string{"Actor Two"}),
	}

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/studios/test-studio/"):
			cards := []cardFixture{
				{url: ts.URL + "/movies/test-001/", label: "TEST-001", thumb: "/thumb/t1.webp", title: "Test Movie One", date: "2026-01-15", studioSlug: "test-studio", studioName: "Test Studio"},
				{url: ts.URL + "/movies/test-002/", label: "TEST-002", thumb: "/thumb/t2.webp", title: "Test Movie Two", date: "2026-01-10", studioSlug: "test-studio", studioName: "Test Studio"},
			}
			_, _ = w.Write(listingHTML(cards, 0))
		default:
			if body, ok := detailBodies[r.URL.Path]; ok {
				_, _ = w.Write(body)
			} else {
				http.NotFound(w, r)
			}
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/studios/test-studio/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	got := map[string]string{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Logf("error: %v", r.Err)
		case scraper.KindScene:
			got[r.Scene.ID] = r.Scene.Title
		}
	}

	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(got), got)
	}
	if got["TEST-001"] != "Full Title One" {
		t.Errorf("scene TEST-001 title = %q, want %q", got["TEST-001"], "Full Title One")
	}
	if got["TEST-002"] != "Full Title Two" {
		t.Errorf("scene TEST-002 title = %q, want %q", got["TEST-002"], "Full Title Two")
	}
}

// ---- TestListScenesKnownIDs ----

func TestListScenesKnownIDs(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/studios/"):
			cards := []cardFixture{
				{url: ts.URL + "/movies/test-001/", label: "TEST-001", thumb: "/t1.webp", title: "One", date: "2026-01-15", studioSlug: "s", studioName: "S"},
				{url: ts.URL + "/movies/test-002/", label: "TEST-002", thumb: "/t2.webp", title: "Two", date: "2026-01-10", studioSlug: "s", studioName: "S"},
			}
			_, _ = w.Write(listingHTML(cards, 0))
		case strings.HasPrefix(r.URL.Path, "/movies/"):
			_, _ = w.Write(censoredDetailHTML("Detail", "TEST-001", "2026-01-15", "120 min.", "Studio", "", "", nil, nil))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/studios/test-studio/", scraper.ListOpts{
		KnownIDs: map[string]bool{"TEST-002": true},
	})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var scenes []scraper.SceneResult
	sawStoppedEarly := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindStoppedEarly:
			sawStoppedEarly = true
		case scraper.KindScene:
			scenes = append(scenes, r)
		}
	}

	if !sawStoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(scenes) != 1 {
		t.Errorf("got %d scenes, want 1 (early stop)", len(scenes))
	}
	if len(scenes) > 0 && scenes[0].Scene.ID != "TEST-001" {
		t.Errorf("scene ID = %q, want TEST-001", scenes[0].Scene.ID)
	}
}

// ---- TestNormalizeSpace ----

func TestNormalizeSpace(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"  hello  world  ", "hello world"},
		{"\n  multiline\n  text  \n", "multiline text"},
		{"", ""},
		{"single", "single"},
	}
	for _, c := range cases {
		got := normalizeSpace(c.input)
		if got != c.want {
			t.Errorf("normalizeSpace(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// The walk skips ids it has already seen, but that alone never ends it: an
// origin that clamps `/page/N/` back to page 1 serves the same cards forever
// while the loop keeps incrementing.
func TestListScenesStopsWhenPagingIsClamped(t *testing.T) {
	var requests int
	var mu sync.Mutex

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/movies/") {
			_, _ = w.Write(censoredDetailHTML("Full Title One", "TEST-001", "2026-01-15",
				"120 min.", "Test Studio", "", "", []string{"Tag A"}, []string{"Actor One"}))
			return
		}
		mu.Lock()
		requests++
		mu.Unlock()
		// Every page — including /page/2/ and beyond — is page 1, and the
		// pager keeps claiming there are more.
		cards := []cardFixture{{
			url: ts.URL + "/movies/test-001/", label: "TEST-001", thumb: "/thumb/t1.webp",
			title: "Test Movie One", date: "2026-01-15",
			studioSlug: "test-studio", studioName: "Test Studio",
		}}
		_, _ = w.Write(listingHTML(cards, 99))
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/studios/test-studio/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan map[string]bool, 1)
	go func() {
		got := map[string]bool{}
		for r := range ch {
			if r.Kind == scraper.KindScene {
				got[r.Scene.ID] = true
			}
		}
		done <- got
	}()

	select {
	case got := <-done:
		if len(got) != 1 {
			t.Errorf("got %d scenes, want 1", len(got))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the walk did not terminate against a clamped pager")
	}

	mu.Lock()
	defer mu.Unlock()
	if requests > 2 {
		t.Errorf("made %d listing requests, want the walk to stop on the first repeat", requests)
	}
}
