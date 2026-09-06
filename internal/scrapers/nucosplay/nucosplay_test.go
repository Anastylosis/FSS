package nucosplay

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://nucosplay.com/", true},
		{"https://www.nucosplay.com/mary-02/", true},
		{"https://nucosplay.com/pornstar/mary/", true},
		{"https://nucosplay.com.evil.org/", false},
		{"https://cosplay.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestSlugOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://nucosplay.com/mary-02/", "mary-02"},
		{"https://nucosplay.com/sunako-k-11/", "sunako-k-11"},
	}
	for _, c := range cases {
		if got := slugOf(c.in); got != c.want {
			t.Errorf("slugOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

const detailPage = `<html><head>
<meta property="og:image" content="https://static.nucosplay.com/cache/0x0/70/content/videos/Mary/02/big.jpg">
<meta name="description" content="This blonde babe wanted to cosplay Mary Satome because it&#8217;s her favorite anime character. She even came with this ">
</head><body>
<p>Search &amp; Explore NuCosplay &raquo; Cosplay Models &raquo; Mary &raquo; Mary Cosplays Mary Satome &raquo; NuCosplay &raquo; a very long breadcrumb that is longer than the scene copy by a comfortable margin indeed</p>
<h1>Mary Cosplays Mary Satome And Plays With Two Toys</h1>
<p>WARNING This site is for adults only! This website contains age-restricted materials including nudity and explicit depictions of sexual activity, and it goes on at considerable length.</p>
<div class="join"><a href="https://join.nucosplay.com/signup/signup.php">Join Nu Cosplay Now!</a></div>
<p>This blonde babe wanted to cosplay <a title="Mary" href="https://nucosplay.com/pornstar/mary/">Mary</a> Satome because it&#8217;s her favorite anime character<span onclick="displayReadMore('abc')" class="dots" id="dotsabc">... <b>Read More</b></span><span class="readmore" id="readmoreabc">. And then the real fun started.</span></p>
<hr>
<ul>
	<li><div class="video-duration"><svg><use xlink:href="#clocksvg"></use></svg>21:02</div></li>
	<li class="middle"><div class="video-date"><svg><use xlink:href="#calendarsvg"></use></svg>November 26th, 2023</div></li>
</ul>
<hr>
<div class="cat"><svg><use xlink:href="#profile"></use></svg><a href="https://nucosplay.com/pornstar/mary/" rel="tag">Mary</a></div>
<div class="cat"><svg><use xlink:href="#tagsvg"></use></svg>Anime
	<a href="https://nucosplay.com/category/categories/close-up/" rel="tag">Close Up</a>
	<a href="https://nucosplay.com/category/body/small-tits/" rel="tag">Small Tits</a>
	<a href="https://nucosplay.com/category/categories/close-up/" rel="tag">Close Up</a>
</div>
</body></html>`

func TestParseScene(t *testing.T) {
	sc, err := parseScene([]byte(detailPage), "https://nucosplay.com/mary-02/", "https://nucosplay.com/")
	if err != nil {
		t.Fatal(err)
	}
	if sc.ID != "mary-02" || sc.SiteID != siteID {
		t.Errorf("id/site = %q/%q", sc.ID, sc.SiteID)
	}
	if sc.Title != "Mary Cosplays Mary Satome And Plays With Two Toys" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.Duration != 21*60+2 {
		t.Errorf("duration = %d", sc.Duration)
	}
	// The day is written with an ordinal suffix, which no Go layout parses.
	if sc.Date.Format("2006-01-02") != "2023-11-26" {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Thumbnail != "https://static.nucosplay.com/cache/0x0/70/content/videos/Mary/02/big.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if !slices.Equal(sc.Performers, []string{"Mary"}) {
		t.Errorf("performers = %v", sc.Performers)
	}
	if !slices.Equal(sc.Categories, []string{"Close Up", "Small Tits"}) {
		t.Errorf("categories = %v — a repeated tag must not be stored twice", sc.Categories)
	}
}

// The scene copy has no class of its own and is neither the longest paragraph
// on the page nor a well-formed one: the breadcrumb and the age warning both
// beat it on length, and the "Read More" toggle splits it in two.
func TestDescriptionIsTheParagraphBeforeTheMetadataStrip(t *testing.T) {
	got := description([]byte(detailPage))
	want := "This blonde babe wanted to cosplay Mary Satome because it’s her favorite anime character. And then the real fun started."
	if got != want {
		t.Errorf("description = %q,\nwant %q", got, want)
	}
	if strings.Contains(got, "Read More") {
		t.Error("the Read More toggle survived into the description")
	}
	if strings.Contains(got, "WARNING") || strings.Contains(got, "Explore") {
		t.Error("description picked up page chrome")
	}
	if got := description([]byte(`<html><body><p>no metadata strip</p></body></html>`)); got != "" {
		t.Errorf("description = %q, want empty when the anchor is missing", got)
	}
}

func TestParseSceneWithoutATitleIsAParseError(t *testing.T) {
	_, err := parseScene([]byte(`<html><body><p>nothing</p></body></html>`),
		"https://nucosplay.com/x-1/", "https://nucosplay.com/")
	if err == nil {
		t.Fatal("want an error")
	}
	if k := scraper.Classify(err); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

// ---- end-to-end ----

type fakeSite struct {
	slugs []string
	// parts controls how many vms_videos sitemap files the index names.
	parts int

	mu   sync.Mutex
	hits []string
}

func (f *fakeSite) hit(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits = append(f.hits, p)
}

func (f *fakeSite) hitList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.hits)
}

func (f *fakeSite) handler(t *testing.T, base func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.hit(r.URL.Path)
		switch {
		case r.URL.Path == sitemapIndex:
			var b strings.Builder
			b.WriteString(`<?xml version="1.0"?><sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
			fmt.Fprintf(&b, `<sitemap><loc>%s/wp-sitemap-posts-page-1.xml</loc></sitemap>`, base())
			for i := 1; i <= f.parts; i++ {
				fmt.Fprintf(&b, `<sitemap><loc>%s/wp-sitemap-posts-vms_videos-%d.xml</loc></sitemap>`, base(), i)
			}
			b.WriteString(`</sitemapindex>`)
			_, _ = fmt.Fprint(w, b.String())
		case strings.HasPrefix(r.URL.Path, "/wp-sitemap-posts-vms_videos-"):
			part, _ := strconv.Atoi(strings.TrimSuffix(
				strings.TrimPrefix(r.URL.Path, "/wp-sitemap-posts-vms_videos-"), ".xml"))
			var b strings.Builder
			b.WriteString(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
			for i, slug := range f.slugs {
				if i%f.parts == part-1 {
					fmt.Fprintf(&b, `<url><loc>%s/%s/</loc></url>`, base(), slug)
				}
			}
			b.WriteString(`</urlset>`)
			_, _ = fmt.Fprint(w, b.String())
		case strings.HasPrefix(r.URL.Path, "/pornstar/"):
			var b strings.Builder
			for _, slug := range f.slugs {
				fmt.Fprintf(&b, `<a href="%s/%s/">x</a>`, base(), slug)
			}
			_, _ = fmt.Fprint(w, "<html><body>"+b.String()+"</body></html>")
		default:
			slug := strings.Trim(r.URL.Path, "/")
			if !slices.Contains(f.slugs, slug) {
				http.NotFound(w, r)
				return
			}
			_, _ = fmt.Fprint(w, strings.Replace(detailPage,
				"Mary Cosplays Mary Satome And Plays With Two Toys", "Scene "+slug, 1))
		}
	}
}

func newTestScraper(t *testing.T, f *fakeSite) *Scraper {
	t.Helper()
	var s *Scraper
	ts := httptest.NewServer(f.handler(t, func() string { return s.base }))
	t.Cleanup(ts.Close)
	s = &Scraper{Client: ts.Client(), base: ts.URL}
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string) ([]string, []error) {
	t.Helper()
	out := make(chan scraper.SceneResult, 500)
	go s.run(context.Background(), studioURL, scraper.ListOpts{}, out)
	var ids []string
	var errs []error
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	slices.Sort(ids)
	return ids, errs
}

// WordPress splits a post type across numbered sitemap files once it grows, so
// reading only the first part would silently drop the rest of the catalogue.
func TestRunReadsEveryVideoSitemapPart(t *testing.T) {
	f := &fakeSite{slugs: []string{"mary-01", "mary-02", "viki-01", "sara-01"}, parts: 2}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://nucosplay.com/")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !slices.Equal(ids, []string{"mary-01", "mary-02", "sara-01", "viki-01"}) {
		t.Errorf("ids = %v", ids)
	}
	hits := f.hitList()
	for i := 1; i <= 2; i++ {
		want := fmt.Sprintf("/wp-sitemap-posts-vms_videos-%d.xml", i)
		if !slices.Contains(hits, want) {
			t.Errorf("never read %s", want)
		}
	}
	// The page sitemap is not a scene list and must not be fetched.
	if slices.Contains(hits, "/wp-sitemap-posts-page-1.xml") {
		t.Error("read the pages sitemap, which holds no scenes")
	}
}

func TestRunPerformerPage(t *testing.T) {
	f := &fakeSite{slugs: []string{"mary-01", "mary-02"}, parts: 1}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, s.base+"/pornstar/mary/")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !slices.Equal(ids, []string{"mary-01", "mary-02"}) {
		t.Errorf("ids = %v", ids)
	}
	if slices.Contains(f.hitList(), sitemapIndex) {
		t.Error("a performer walk must not fall back to the whole catalogue")
	}
}

// A sitemap index that names no video part is a redesign, not an empty
// catalogue — the difference decides whether an authoritative Save may delete.
func TestRunReportsASitemapWithNoVideoPart(t *testing.T) {
	f := &fakeSite{slugs: nil, parts: 0}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://nucosplay.com/")
	if len(ids) != 0 {
		t.Errorf("got %d scenes, want none", len(ids))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}
