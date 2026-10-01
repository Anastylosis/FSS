package utgutil

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

var _ scraper.StudioScraper = (*Scraper)(nil)

var testCfg = SiteConfig{
	SiteID:     "testsite",
	Domain:     "testsite.com",
	StudioName: "Test Studio",
}

const testArticleVideo = `<article class="flex flex-col lift relative">
    <a href="/join" class="relative block">
        <img
                src="https://assets.utgnetworks.com/testsite/images/category/videos/jane_doe_hot_scene/jane_doe_hot_scene.jpg?token=abc123&expires=9999999&class=smallThumb"
                alt="Hot Scene"
                class="w-full h-auto object-cover"
        />
    </a>
    <div class="flex flex-1 flex-col py-2">
        <h3 class="font-heading text-xl text-primary-600">
            <a href="/join">Hot Scene</a>
        </h3>
        <p class="mt-1 text-xs font-bold">20 May 2026</p>
        <p class="text-xs text-primary-600">
            <a href="/models/jane-doe" class="hover:underline">Jane Doe</a>
        </p>
        <p class="text-xs">
            8:58 Minutes | <a href="/join">View Video</a>
        </p>
    </div>
    <div class="update_video_banner"><a href="/join">Hot Scene</a></div>
</article>`

const testArticlePhoto = `<article class="flex flex-col lift relative">
    <a href="/join" class="relative block">
        <img
                src="https://assets.utgnetworks.com/testsite/images/category/photos/jane_doe_studio/jane_doe_studio.jpg?token=xyz789&expires=9999999&class=smallThumb"
                alt="Studio Shoot"
                class="w-full h-auto object-cover"
        />
    </a>
    <div class="flex flex-1 flex-col py-2">
        <h3 class="font-heading text-xl text-primary-600">
            <a href="/join">Studio Shoot</a>
        </h3>
        <p class="mt-1 text-xs font-bold">15 May 2026</p>
        <p class="text-xs text-primary-600">
            <a href="/models/jane-doe" class="hover:underline">Jane Doe</a>
        </p>
        <p class="text-xs">
            56 Photos | <a href="/join">View Photos</a>
        </p>
    </div>
</article>`

func buildPage(articles string, videoCount int) string {
	var sb strings.Builder
	sb.WriteString(`<html><body>`)
	if videoCount > 0 {
		fmt.Fprintf(&sb, `<p>HD VIDEO: %d</p>`, videoCount)
	}
	sb.WriteString(`<div class="grid">`)
	sb.WriteString(articles)
	sb.WriteString(`</div></body></html>`)
	return sb.String()
}

func TestParseArticles_VideoOnly(t *testing.T) {
	body := []byte(buildPage(testArticleVideo+testArticlePhoto, 0))
	articles := parseArticles(body, true)
	if len(articles) != 1 {
		t.Fatalf("expected 1 video article, got %d", len(articles))
	}
	a := articles[0]
	if a.title != "Hot Scene" {
		t.Errorf("title = %q, want %q", a.title, "Hot Scene")
	}
	if a.slug != "jane_doe_hot_scene" {
		t.Errorf("slug = %q, want %q", a.slug, "jane_doe_hot_scene")
	}
	if a.model != "Jane Doe" {
		t.Errorf("model = %q, want %q", a.model, "Jane Doe")
	}
	if a.modelSlug != "jane-doe" {
		t.Errorf("modelSlug = %q, want %q", a.modelSlug, "jane-doe")
	}
	if a.date != "20 May 2026" {
		t.Errorf("date = %q, want %q", a.date, "20 May 2026")
	}
	if a.duration != 538 {
		t.Errorf("duration = %d, want 538", a.duration)
	}
	if !a.isVideo {
		t.Error("isVideo = false, want true")
	}
}

func TestParseArticles_AllTypes(t *testing.T) {
	body := []byte(buildPage(testArticleVideo+testArticlePhoto, 0))
	articles := parseArticles(body, false)
	if len(articles) != 2 {
		t.Fatalf("expected 2 articles, got %d", len(articles))
	}
	if articles[0].isVideo != true {
		t.Error("first article should be video")
	}
	if articles[1].isVideo != false {
		t.Error("second article should not be video")
	}
}

func TestFilterVideos(t *testing.T) {
	articles := []article{
		{title: "Video", isVideo: true},
		{title: "Photo", isVideo: false},
		{title: "Video2", isVideo: true},
	}
	vids := filterVideos(articles)
	if len(vids) != 2 {
		t.Fatalf("expected 2 videos, got %d", len(vids))
	}
}

func TestParseVideoCount(t *testing.T) {
	tests := []struct {
		html string
		want int
	}{
		{`<p>HD VIDEO: 1,025</p>`, 1025},
		{`<p>HD VIDEO: 42</p>`, 42},
		{`<p>no count here</p>`, 0},
	}
	for _, tt := range tests {
		got := parseVideoCount([]byte(tt.html))
		if got != tt.want {
			t.Errorf("parseVideoCount(%q) = %d, want %d", tt.html, got, tt.want)
		}
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"20 May 2026", "2026-05-20"},
		{"1 January 2024", "2024-01-01"},
		{"", "0001-01-01"},
		{"invalid", "0001-01-01"},
	}
	for _, tt := range tests {
		got := parseDate(tt.input)
		want := tt.want
		if got.Format("2006-01-02") != want {
			t.Errorf("parseDate(%q) = %s, want %s", tt.input, got.Format("2006-01-02"), want)
		}
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Hot Scene", "hot-scene"},
		{"Crimson Canvas BTS", "crimson-canvas-bts"},
		{"Hello World!!!", "hello-world"},
		{"A--B", "a-b"},
		{" Leading Trailing ", "leading-trailing"},
	}
	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToScene(t *testing.T) {
	a := article{
		title:     "Hot Scene",
		slug:      "jane_doe_hot_scene",
		thumbnail: "https://cdn.example.com/thumb.jpg?token=abc",
		date:      "20 May 2026",
		duration:  538,
		model:     "Jane Doe",
		modelSlug: "jane-doe",
		isVideo:   true,
	}
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	scene := a.toScene(testCfg, "https://testsite.com/updates/videos", now)

	if scene.ID != "jane_doe_hot_scene" {
		t.Errorf("ID = %q, want %q", scene.ID, "jane_doe_hot_scene")
	}
	if scene.SiteID != "testsite" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Title != "Hot Scene" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Duration != 538 {
		t.Errorf("Duration = %d", scene.Duration)
	}
	if scene.Thumbnail != "https://cdn.example.com/thumb.jpg" {
		t.Errorf("Thumbnail = %q (token not stripped?)", scene.Thumbnail)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Jane Doe" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if scene.Studio != "Test Studio" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if scene.Date.Format("2006-01-02") != "2026-05-20" {
		t.Errorf("Date = %s", scene.Date)
	}
}

func TestSlugFromCDN(t *testing.T) {
	body := []byte(`<article><img src="https://assets.utgnetworks.com/site/images/category/videos/my_slug/my_slug.jpg?token=x" alt="Title"/><div class="update_video_banner"></div></article>`)
	articles := parseArticles(body, true)
	if len(articles) != 1 {
		t.Fatalf("expected 1, got %d", len(articles))
	}
	if articles[0].slug != "my_slug" {
		t.Errorf("slug = %q, want %q", articles[0].slug, "my_slug")
	}
}

func TestSlugFallbackToTitle(t *testing.T) {
	body := []byte(`<article><img src="https://other.com/thumb.jpg" alt="My Title"/><div class="update_video_banner"></div></article>`)
	articles := parseArticles(body, true)
	if len(articles) != 1 {
		t.Fatalf("expected 1, got %d", len(articles))
	}
	if articles[0].slug != "my-title" {
		t.Errorf("slug = %q, want %q", articles[0].slug, "my-title")
	}
}

func collect(t *testing.T, ch <-chan scraper.SceneResult) []scraper.SceneResult {
	t.Helper()
	var results []scraper.SceneResult
	for r := range ch {
		results = append(results, r)
	}
	return results
}

func testScraper(ts *httptest.Server) *Scraper {
	return &Scraper{
		client:       ts.Client(),
		cfg:          testCfg,
		baseOverride: ts.URL,
	}
}

func TestRunPaginated(t *testing.T) {
	page1 := buildPage(testArticleVideo, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/updates/videos":
			_, _ = fmt.Fprint(w, page1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := testScraper(ts)

	ctx := context.Background()
	ch, err := s.ListScenes(ctx, ts.URL+"/updates/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(t, ch)
	var scenes int
	for _, r := range results {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
}

func TestRunModel(t *testing.T) {
	modelPage := buildPage(testArticleVideo+testArticlePhoto, 0)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/jane-doe":
			_, _ = fmt.Fprint(w, modelPage)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := testScraper(ts)

	ctx := context.Background()
	ch, err := s.ListScenes(ctx, ts.URL+"/models/jane-doe", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(t, ch)
	var scenes int
	for _, r := range results {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes from model page, want 1 (only videos)", scenes)
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	page := buildPage(testArticleVideo, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, page)
	}))
	defer ts.Close()

	s := testScraper(ts)

	ctx := context.Background()
	ch, err := s.ListScenes(ctx, ts.URL+"/updates/videos", scraper.ListOpts{
		KnownIDs: map[string]bool{"jane_doe_hot_scene": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(t, ch)
	var stoppedEarly bool
	for _, r := range results {
		if r.Kind == scraper.KindStoppedEarly {
			stoppedEarly = true
		}
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly result")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(testCfg)
	tests := []struct {
		url  string
		want bool
	}{
		{"https://testsite.com/updates/videos", true},
		{"https://www.testsite.com/updates/videos", true},
		{"https://testsite.com/models/jane-doe", true},
		{"https://testsite.com/", true},
		{"https://testsite.com", true},
		{"https://othersite.com/updates/videos", false},
	}
	for _, tt := range tests {
		got := s.MatchesURL(tt.url)
		if got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestPatterns(t *testing.T) {
	s := New(testCfg)
	pats := s.Patterns()
	if len(pats) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(pats))
	}
	if !strings.Contains(pats[0], "/updates/videos") {
		t.Errorf("pattern[0] = %q", pats[0])
	}
	if !strings.Contains(pats[1], "/models/") {
		t.Errorf("pattern[1] = %q", pats[1])
	}
}

func TestProgressSent(t *testing.T) {
	page := buildPage(testArticleVideo, 42)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/updates/videos":
			_, _ = fmt.Fprint(w, page)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := testScraper(ts)

	ctx := context.Background()
	ch, err := s.ListScenes(ctx, ts.URL+"/updates/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(t, ch)
	var totalSent bool
	for _, r := range results {
		if r.Kind == scraper.KindTotal && r.Total == 42 {
			totalSent = true
		}
	}
	if !totalSent {
		t.Error("expected Progress(42) result")
	}
}

// --- Legacy template tests ---

const legacyEntryWithDuration = `<div class="single_update">
<div class="update_video_banner"><a href="/join">Great Scene</a></div>
<div class="cover_image">
<a href="/join"><img class="img-responsive" src="https://assets.utgnetworks.com/testsite/images/category/videos/jane_great_scene/jane_great_scene.jpg?token=abc&expires=999&class=smallThumb" alt="Great Scene" /></a>
</div>
<div class="update_desc">
<p class="right">May 2025</p>
<p class="set_title"><a href="/join"><strong>Great Scene</strong></a><p>
<p class="count">5:03 Minutes | <a href="/join">View Video</a></p>
</div>
</div>`

const legacyEntryNoDuration = `<div class="box2">
<a href="/join">
<img src="https://assets.utgnetworks.com/testsite/images/category/videos/solo_shot/solo_shot.jpg?token=xyz&class=smallThumb" alt="Solo Shot" />
<span class="vid-overlay"><i class="fa fa-youtube-play"></i></span>
</a>
<div class="caption"><p class="text_6">Solo Shot</p></div>
</div>`

func TestParseLegacyArticles(t *testing.T) {
	body := []byte(legacyEntryWithDuration + legacyEntryNoDuration)
	articles := parseLegacyArticles(body)
	if len(articles) != 2 {
		t.Fatalf("expected 2 articles, got %d", len(articles))
	}

	a := articles[0]
	if a.slug != "jane_great_scene" {
		t.Errorf("slug = %q, want %q", a.slug, "jane_great_scene")
	}
	if a.title != "Great Scene" {
		t.Errorf("title = %q, want %q", a.title, "Great Scene")
	}
	if a.duration != 303 {
		t.Errorf("duration = %d, want 303", a.duration)
	}

	b := articles[1]
	if b.slug != "solo_shot" {
		t.Errorf("slug = %q, want %q", b.slug, "solo_shot")
	}
	if b.duration != 0 {
		t.Errorf("duration = %d, want 0", b.duration)
	}
}

func TestParseLegacyYears(t *testing.T) {
	body := []byte(`<a href="/updates/videos/25">2025</a><a href="/updates/videos/24">2024</a><a href="/updates/videos/23">2023</a>`)
	years := parseLegacyYears(body)
	if len(years) != 3 {
		t.Fatalf("expected 3 years, got %d", len(years))
	}
	if years[0] != 25 || years[1] != 24 || years[2] != 23 {
		t.Errorf("years = %v, want [25 24 23]", years)
	}
}

func TestParseLegacyMaxPage(t *testing.T) {
	body := []byte(`<a href="/updates/videos/1">1</a><a href="/updates/videos/2">2</a><a href="/updates/videos/3">3</a>`)
	maxPage := parseLegacyMaxPage(body)
	if maxPage != 3 {
		t.Errorf("maxPage = %d, want 3", maxPage)
	}
}

func TestRunLegacyPages(t *testing.T) {
	page := legacyEntryWithDuration + legacyEntryNoDuration

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/updates/videos/1/200" {
			_, _ = fmt.Fprint(w, page)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{
		client:       ts.Client(),
		cfg:          SiteConfig{SiteID: "testlegacy", Domain: "test.com", StudioName: "Test", Legacy: true},
		baseOverride: ts.URL,
	}

	ctx := context.Background()
	ch, err := s.ListScenes(ctx, ts.URL+"/updates/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(t, ch)
	var scenes int
	for _, r := range results {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

func TestRunLegacyYears(t *testing.T) {
	index := `<a href="/updates/videos/25">2025</a><a href="/updates/videos/24">2024</a>`
	year25 := legacyEntryWithDuration
	year24 := legacyEntryNoDuration

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/updates/videos":
			_, _ = fmt.Fprint(w, index)
		case "/updates/videos/25":
			_, _ = fmt.Fprint(w, year25)
		case "/updates/videos/24":
			_, _ = fmt.Fprint(w, year24)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{
		client:       ts.Client(),
		cfg:          SiteConfig{SiteID: "testyears", Domain: "test.com", StudioName: "Test", Legacy: true, YearBased: true},
		baseOverride: ts.URL,
	}

	ctx := context.Background()
	ch, err := s.ListScenes(ctx, ts.URL+"/updates/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(t, ch)
	var scenes int
	for _, r := range results {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

// --- Livewire template variants (2026 redesign) ---

// Hayley's Secrets / Girlfolio / BreathTakers: absolute model and preview links.
const lwArticleAbsolute = `<article
    wire:key="update-2726"
    class="flex flex-col lift relative"
>
    <a href="https://www.testsite.com/updates/previews/videos/seductions-of-water" class="relative block">
        <img
            src="https://assets.utgnetworks.com/testsite/images/category/videos/ruby_mae_seductions_of_water/ruby_mae_seductions_of_water.jpg?class=blurThumbNsfw"
            alt="Seductions Of Water"
            class="w-full aspect-3/2 h-auto object-cover blur-lg"
        />
    </a>
    <div class="flex flex-1 flex-col py-2">
        <h3 class="font-heading text-xl text-primary-600 mb-0!">
            <a href="https://www.testsite.com/updates/previews/videos/seductions-of-water" class="hover:underline focus:underline">Seductions Of Water</a>
        </h3>
        <p class="mt-1 text-xs font-bold mb-0.5">09 September 2026</p>
        <p class="text-xs text-primary-600 mb-0.5">
            <a class="hover:underline focus:underline" href="https://www.testsite.com/models/ruby-mae">Ruby Mae</a>
        </p>
        <p class="text-xs mb-0.5">
            8:25 Minutes | <a href="https://www.testsite.com/updates/previews/videos/seductions-of-water">View Video</a>
        </p>
    </div>
    <div class="update_videos_banner"><a href="https://www.testsite.com/updates/previews/videos/seductions-of-water">Seductions Of Water</a></div>
</article>`

// More Than Nylons: no duration and no banner; a sub-category link.
const lwArticleNoDuration = `<article class="lift">
    <a href="https://www.testsite.com/updates/previews/videos/natures-secrets-bts" class="block relative">
            <img
                src="https://assets.utgnetworks.com/testsite/images/category/videos/blossom_may_natures_secrets_bts/blossom_may_natures_secrets_bts_l.jpg?class=smallThumb"
                alt="Natures Secrets BTS"
                class="w-full aspect-3/2 md:h-full object-cover"
            />
    </a>
    <h3 class="mt-0.5 text-sm sm:text-xs font-bold mb-0!">
        <a class="hover:underline focus:underline" href="https://www.testsite.com/models/blossom-may">Blossom May</a>
    </h3>
    <div class="flex justify-between">
        <h3 class="mt-1 text-sm sm:text-xs font-bold sm:text-right">
            Category:
            <a class="hover:underline focus:underline" href="https://www.testsite.com/sub-category/stockings">Stockings</a>
        </h3>
        <h3 class="mt-1 text-sm sm:text-xs font-bold sm:text-right">15 September 2026</h3>
    </div>
</article>`

// UK Tickling: links go to /join; the model is only named in an overlay span.
const lwArticleAngel = `<article class="lift relative">
    <a href="https://www.testsite.com/join" class="relative block">
            <img
                src="https://assets.utgnetworks.com/testsite/images/category/videos/shante_mai_ukt_1838/a16c6c7c.jpg?class=smallThumb"
                alt="Ukt 1838"
                class="border-2 rounded-xl w-full object-cover"
            />
                <div class="absolute bottom-0 right-4 max-w-[70%]">
                    <span
                        class="angel-name block text-right font-angel text-pink-400 whitespace-nowrap"
                    >
                        Shante Mai
                    </span>
                </div>
    </a>
    <div class="update_videos_banner"><a href="https://www.testsite.com/join">Ukt 1838</a></div>
</article>`

// A model page's photo set: must be filtered out.
const lwArticlePhoto = `<article class="flex flex-col lift relative">
    <a href="https://www.testsite.com/updates/previews/photos/style-and-skin" class="relative block">
        <img
            src="https://assets.utgnetworks.com/testsite/images/category/photos/ruby_mae_style/ruby_mae_style.jpg?class=smallThumb"
            alt="Style and Skin"
        />
    </a>
    <p class="mt-1 text-xs font-bold mb-0.5">01 September 2026</p>
    <div class="update_gallery_banner"><a href="https://www.testsite.com/updates/previews/photos/style-and-skin">Style and Skin</a></div>
</article>`

func lwPager(last int) string {
	return fmt.Sprintf(`<button type="button" wire:click="gotoPage(2, 'updates_page')">2</button><button type="button" wire:click="gotoPage(%d, 'updates_page')">%d</button>`, last, last)
}

func TestParseArticles_LivewireVariants(t *testing.T) {
	body := buildPage(lwArticleAbsolute+lwArticleNoDuration+lwArticleAngel+lwArticlePhoto, 0)
	arts := parseArticles([]byte(body), true)
	if len(arts) != 3 {
		t.Fatalf("got %d video articles, want 3 (photo set excluded)", len(arts))
	}
	now := time.Now().UTC()

	a := arts[0].toScene(testCfg, "https://testsite.com/updates/videos", now)
	if a.ID != "ruby_mae_seductions_of_water" || a.Title != "Seductions Of Water" {
		t.Errorf("ID/Title = %q/%q", a.ID, a.Title)
	}
	if a.URL != "https://www.testsite.com/updates/previews/videos/seductions-of-water" {
		t.Errorf("URL = %q, want the article's own preview link", a.URL)
	}
	if len(a.Performers) != 1 || a.Performers[0] != "Ruby Mae" {
		t.Errorf("Performers = %v (absolute /models/ link)", a.Performers)
	}
	if a.Duration != 505 {
		t.Errorf("Duration = %d, want 505", a.Duration)
	}
	if want := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC); !a.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", a.Date, want)
	}

	b := arts[1].toScene(testCfg, "", now)
	if b.ID != "blossom_may_natures_secrets_bts" || len(b.Performers) != 1 || b.Performers[0] != "Blossom May" {
		t.Errorf("ID/Performers = %q/%v", b.ID, b.Performers)
	}
	if len(b.Categories) != 1 || b.Categories[0] != "Stockings" {
		t.Errorf("Categories = %v", b.Categories)
	}

	c := arts[2].toScene(testCfg, "", now)
	if c.ID != "shante_mai_ukt_1838" || len(c.Performers) != 1 || c.Performers[0] != "Shante Mai" {
		t.Errorf("ID/Performers = %q/%v (angel-name overlay)", c.ID, c.Performers)
	}
	if c.URL != "https://testsite.com/updates/previews/videos/ukt-1838" {
		t.Errorf("URL = %q, want the title-derived fallback when the card links to /join", c.URL)
	}
}

func TestParseLastPage(t *testing.T) {
	if got := parseLastPage([]byte(lwPager(26))); got != 26 {
		t.Errorf("parseLastPage = %d, want 26", got)
	}
	if got := parseLastPage([]byte(`wire:click="gotoPage(7, &#039;updates_page&#039;)"`)); got != 7 {
		t.Errorf("parseLastPage (escaped) = %d, want 7", got)
	}
	if got := parseLastPage([]byte(`<p>no pager</p>`)); got != 0 {
		t.Errorf("parseLastPage = %d, want 0", got)
	}
}

func TestRunListing_followsUpdatesPage(t *testing.T) {
	var queries []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/updates/videos" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		switch r.URL.Query().Get("updates_page") {
		case "":
			_, _ = fmt.Fprint(w, buildPage(lwArticleAbsolute+lwPager(2), 0))
		case "2":
			_, _ = fmt.Fprint(w, buildPage(lwArticleNoDuration+lwPager(2), 0))
		default:
			t.Errorf("unexpected page %q", r.URL.RawQuery)
		}
	}))
	defer ts.Close()

	ch, err := testScraper(ts).ListScenes(context.Background(), ts.URL+"/updates/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	total := 0
	for _, r := range collect(t, ch) {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindTotal:
			total = r.Total
		}
	}
	if fmt.Sprint(ids) != "[ruby_mae_seductions_of_water blossom_may_natures_secrets_bts]" {
		t.Errorf("ids = %v", ids)
	}
	if fmt.Sprint(queries) != "[ updates_page=2]" {
		t.Errorf("queries = %q, want page 1 bare then updates_page=2 and no further", queries)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (last page x page size)", total)
	}
}

func TestRunListing_modelPageOfPhotosContinues(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/ruby-mae" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("updates_page") {
		case "":
			_, _ = fmt.Fprint(w, buildPage(lwArticlePhoto+lwPager(2), 0))
		case "2":
			_, _ = fmt.Fprint(w, buildPage(lwArticleAbsolute+lwPager(2), 0))
		}
	}))
	defer ts.Close()

	ch, err := testScraper(ts).ListScenes(context.Background(), ts.URL+"/models/ruby-mae", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range collect(t, ch) {
		if r.Kind == scraper.KindScene {
			ids = append(ids, r.Scene.ID)
		}
	}
	if fmt.Sprint(ids) != "[ruby_mae_seductions_of_water]" {
		t.Errorf("ids = %v, want the video from page 2 after a photo-only page 1", ids)
	}
}
