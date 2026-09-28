package gasm

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

func TestMatchesURL(t *testing.T) {
	t.Parallel()
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.gasm.com/studio/profile/harmonyvision", true},
		{"https://gasm.com/studio/profile/cosplaybabes", true},
		{"http://www.gasm.com/studio/profile/magmafilm", true},
		{"https://www.harmonyvision.com/", true},
		{"https://harmonyvision.com", true},
		{"https://www.cosplaybabes.xxx/", true},
		{"https://www.magmafilm.com", true},
		{"https://www.mmvfilms.de/", true},
		{"https://www.purexxxfilms.com/", true},
		{"https://www.gasm.com/", false},
		{"https://www.gasm.com/post/details/12345", false},
		{"https://example.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestResolveSlug(t *testing.T) {
	t.Parallel()
	cases := []struct {
		url      string
		wantSlug string
		wantErr  bool
	}{
		{"https://www.gasm.com/studio/profile/harmonyvision", "harmonyvision", false},
		{"https://gasm.com/studio/profile/cosplaybabes", "cosplaybabes", false},
		{"https://www.harmonyvision.com/", "harmonyvision", false},
		{"https://www.cosplaybabes.xxx/", "cosplaybabes", false},
		{"https://www.mmvfilms.de/", "mmvfilms", false},
		{"https://www.magmafilm.com", "magmafilm", false},
		{"https://example.com", "", true},
	}
	for _, c := range cases {
		slug, err := resolveSlug(c.url)
		if c.wantErr {
			if err == nil {
				t.Errorf("resolveSlug(%q) expected error", c.url)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveSlug(%q) error: %v", c.url, err)
			continue
		}
		if slug != c.wantSlug {
			t.Errorf("resolveSlug(%q) = %q, want %q", c.url, slug, c.wantSlug)
		}
	}
}

const profileHTML = `<html>
<a class="user_profile_menu" data-tab="videos">VIDEOS <span>(3)</span></a>
<a class="user_profile_menu" data-tab="galleries">GALLERIES <span>(5)</span></a>
<div class="_xlabs_results_wrapper _feed"
     data-ajax-params="{&quot;contentType&quot;:&quot;posts&quot;,&quot;user_ids&quot;:[42],&quot;sorting&quot;:&quot;date_and_id&quot;}">
</div>
</html>`

func TestParseProfile(t *testing.T) {
	t.Parallel()
	userID, videoCount, err := parseProfile([]byte(profileHTML), "teststudio")
	if err != nil {
		t.Fatalf("parseProfile: %v", err)
	}
	if userID != 42 {
		t.Errorf("userID = %d, want 42", userID)
	}
	if videoCount != 3 {
		t.Errorf("videoCount = %d, want 3", videoCount)
	}
}

func TestParseProfileMissingParams(t *testing.T) {
	t.Parallel()
	_, _, err := parseProfile([]byte("<html></html>"), "missing")
	if err == nil {
		t.Error("expected error for page without data-ajax-params")
	}
}

const listingHTML = `<div class="_results _results_posts">
<div class="_results_item _results_posts_item">
<div class="post_item video" data-post-id="100">
<a class="preview" href="/post/details/100"
   data-media-poster="https://cdn.example.com/thumb100.jpeg">
<img class="_image item_cover" src="https://cdn.example.com/thumb100.jpeg"/>
</a>
<span class="counter"><i class="far fa-clock"></i> <b>12:34</b></span>
<a class="post_title" href="/post/details/100" title="First Scene">First Scene</a>
<a class="post_channel" href="/studio/profile/teststudio">Channel: teststudio</a>
</div></div></div>
<div class="_results_item _results_posts_item">
<div class="post_item video" data-post-id="200">
<a class="preview" href="/post/details/200"
   data-media-poster="https://cdn.example.com/thumb200.jpeg">
<img class="_image item_cover" src="https://cdn.example.com/thumb200.jpeg"/>
</a>
<span class="counter"><i class="far fa-clock"></i> <b>05:00</b></span>
<a class="post_title" href="/post/details/200" title="Second Scene">Second Scene</a>
<a class="post_channel" href="/studio/profile/teststudio">Channel: teststudio</a>
</div></div></div>
<div class="_pagination"><div class="paginationArea">
<a class="highlight">1</a>
<a href="?page=2" data-page="2" class="pageBtn" title="last">last</a>
</div></div>`

func TestParseListing(t *testing.T) {
	t.Parallel()
	items, totalPages, err := parseListing([]byte(listingHTML))
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}

	if totalPages != 2 {
		t.Errorf("totalPages = %d, want 2", totalPages)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	if items[0].id != "100" {
		t.Errorf("items[0].id = %q, want 100", items[0].id)
	}
	if items[0].title != "First Scene" {
		t.Errorf("items[0].title = %q", items[0].title)
	}
	if items[0].thumbnail != "https://cdn.example.com/thumb100.jpeg" {
		t.Errorf("items[0].thumbnail = %q", items[0].thumbnail)
	}
	if items[0].duration != "12:34" {
		t.Errorf("items[0].duration = %q", items[0].duration)
	}

	if items[1].id != "200" {
		t.Errorf("items[1].id = %q, want 200", items[1].id)
	}
	if items[1].title != "Second Scene" {
		t.Errorf("items[1].title = %q", items[1].title)
	}
}

func TestParseListingEmpty(t *testing.T) {
	t.Parallel()
	items, totalPages, err := parseListing([]byte(`<div class="_results"></div>`))
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("got %d items, want 0", len(items))
	}
	if totalPages != 0 {
		t.Errorf("totalPages = %d, want 0", totalPages)
	}
}

func TestParseListingDeduplicates(t *testing.T) {
	t.Parallel()
	body := `<div class="post_item video" data-post-id="100">
<a class="post_title" href="/post/details/100" title="Dupe">Dupe</a>
</div></div></div>
<div class="post_item video" data-post-id="100">
<a class="post_title" href="/post/details/100" title="Dupe">Dupe</a>
</div></div></div>`
	items, _, err := parseListing([]byte(body))
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("got %d items, want 1 (deduped)", len(items))
	}
}

const detailHTML = `<html>
<h1 class="post_title"><span>Test Scene Title</span></h1>
<span dev-span="{&quot;id&quot;:100,&quot;title&quot;:&quot;Test Scene Title&quot;,&quot;slug&quot;:&quot;test-scene-title&quot;,&quot;body&quot;:&quot;A great description.&quot;,&quot;publishdate&quot;:{&quot;date&quot;:&quot;2025-03-15 00:00:00.000000&quot;,&quot;timezone_type&quot;:3,&quot;timezone&quot;:&quot;America/Detroit&quot;},&quot;duration&quot;:&quot;12:34&quot;,&quot;cover&quot;:&quot;media/gasm/studios/teststudio/videos/cover.jpeg&quot;,&quot;tokens_price&quot;:15,&quot;owner&quot;:{&quot;username&quot;:&quot;teststudio&quot;,&quot;id&quot;:42},&quot;actors&quot;:[{&quot;id&quot;:1,&quot;name&quot;:&quot;Jane Doe&quot;,&quot;slug&quot;:&quot;jane-doe&quot;},{&quot;id&quot;:2,&quot;name&quot;:&quot;John Smith&quot;,&quot;slug&quot;:&quot;john-smith&quot;}],&quot;tags&quot;:[{&quot;id&quot;:10,&quot;name&quot;:&quot;Hardcore&quot;,&quot;slug&quot;:&quot;hardcore&quot;},{&quot;id&quot;:11,&quot;name&quot;:&quot;Blonde&quot;,&quot;slug&quot;:&quot;blonde&quot;}]}">
</span>
</html>`

func TestParseDetail(t *testing.T) {
	t.Parallel()
	scene, err := parseDetail([]byte(detailHTML), "100",
		"https://www.gasm.com/studio/profile/teststudio", "teststudio")
	if err != nil {
		t.Fatalf("parseDetail: %v", err)
	}

	if scene.ID != "100" {
		t.Errorf("ID = %q, want 100", scene.ID)
	}
	if scene.SiteID != "gasm" {
		t.Errorf("SiteID = %q, want gasm", scene.SiteID)
	}
	if scene.Title != "Test Scene Title" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Description != "A great description." {
		t.Errorf("Description = %q", scene.Description)
	}
	wantDate := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", scene.Date, wantDate)
	}
	if scene.Duration != 754 {
		t.Errorf("Duration = %d, want 754", scene.Duration)
	}
	if scene.Thumbnail != cdnBase+"media/gasm/studios/teststudio/videos/cover.jpeg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if len(scene.Performers) != 2 || scene.Performers[0] != "Jane Doe" || scene.Performers[1] != "John Smith" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if len(scene.Tags) != 2 || scene.Tags[0] != "Hardcore" || scene.Tags[1] != "Blonde" {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Studio != "teststudio" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if scene.URL != "https://www.gasm.com/post/details/100" {
		t.Errorf("URL = %q", scene.URL)
	}
}

const detailHTMLNullDuration = `<html>
<span dev-span="{&quot;id&quot;:200,&quot;title&quot;:&quot;No Duration&quot;,&quot;slug&quot;:&quot;no-duration&quot;,&quot;body&quot;:&quot;&quot;,&quot;publishdate&quot;:{&quot;date&quot;:&quot;2025-01-01 00:00:00.000000&quot;,&quot;timezone_type&quot;:3,&quot;timezone&quot;:&quot;America/Detroit&quot;},&quot;duration&quot;:null,&quot;cover&quot;:&quot;&quot;,&quot;owner&quot;:{&quot;username&quot;:&quot;test&quot;,&quot;id&quot;:1},&quot;actors&quot;:[],&quot;tags&quot;:[]}">
</span>
</html>`

func TestParseDetailNullDuration(t *testing.T) {
	t.Parallel()
	scene, err := parseDetail([]byte(detailHTMLNullDuration), "200",
		"https://www.gasm.com/studio/profile/test", "test")
	if err != nil {
		t.Fatalf("parseDetail: %v", err)
	}
	if scene.Duration != 0 {
		t.Errorf("Duration = %d, want 0 for null", scene.Duration)
	}
}

func TestParseDetailMissingDevSpan(t *testing.T) {
	t.Parallel()
	_, err := parseDetail([]byte("<html></html>"), "999",
		"https://www.gasm.com/studio/profile/test", "test")
	if err == nil {
		t.Error("expected error for page without dev-span")
	}
}

func TestExtractHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		url, want string
	}{
		{"https://www.harmonyvision.com/", "harmonyvision.com"},
		{"https://harmonyvision.com", "harmonyvision.com"},
		{"https://www.cosplaybabes.xxx/foo", "cosplaybabes.xxx"},
		{"https://www.gasm.com/studio/profile/test", "gasm.com"},
		{"", ""},
	}
	for _, c := range cases {
		if got := extractHost(c.url); got != c.want {
			t.Errorf("extractHost(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestSplitCards(t *testing.T) {
	t.Parallel()
	cards := splitCards([]byte(listingHTML))
	if len(cards) != 2 {
		t.Errorf("splitCards returned %d cards, want 2", len(cards))
	}
}

// The network answers with an SFW preview page, not an error, when the request
// comes from an age-verification jurisdiction. Reporting that as a missing
// regex sends the reader after a parser bug that is not there.
func TestParseProfileReportsSFWGate(t *testing.T) {
	const sfw = `<html><head><title>SFW | GASM</title></head><body>
	<p>ATTENTION: You are seeing a preview page for our official adult website.
	Due to your State or Country Age Verification laws, we are not able to show you the goods.</p>
	</body></html>`

	_, _, err := parseProfile([]byte(sfw), "cosplaybabes")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "age-verification blocked") {
		t.Errorf("error = %q, want it to name the geo block", err)
	}

	// A genuinely broken profile page still reports the missing marker.
	_, _, err = parseProfile([]byte("<html><body>redesigned</body></html>"), "cosplaybabes")
	if err == nil || !strings.Contains(err.Error(), "data-ajax-params") {
		t.Errorf("error = %v, want the parse message", err)
	}
}

// The paginate endpoint reports a page count only when it renders a pager, so
// a response that omits one — or an endpoint that ignores aParams[page] — would
// otherwise echo the first page forever. The walk must end on a page that adds
// no new post id.
func TestRunStopsWhenThePaginateEndpointEchoes(t *testing.T) {
	// listingHTML's pager names a "last" page; strip it so totalPages is 0 and
	// only the dedup guard can end the walk.
	echoed := listingHTML[:strings.Index(listingHTML, `<div class="_pagination">`)]

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/studio/profile/"):
			_, _ = fmt.Fprint(w, profileHTML)
		case r.URL.Path == "/op/results/paginate":
			_, _ = fmt.Fprint(w, echoed)
		case strings.HasPrefix(r.URL.Path, "/post/details/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	old := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = old })

	s := New()
	s.client = srv.Client()

	ch, err := s.ListScenes(context.Background(), "https://www.gasm.com/studio/profile/teststudio", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan int)
	go func() {
		n := 0
		for r := range ch {
			if r.Kind == scraper.KindScene {
				n++
			}
		}
		done <- n
	}()

	select {
	case n := <-done:
		if n != 2 {
			t.Errorf("got %d scenes, want 2 — every page after the first repeats", n)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the walk did not terminate against an echoing paginate endpoint")
	}
}
