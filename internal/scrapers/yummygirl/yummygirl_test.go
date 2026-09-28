package yummygirl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

var _ scraper.StudioScraper = (*Scraper)(nil)

func TestMatchesURL(t *testing.T) {
	s := New()
	yes := []string{
		"https://yummygirl.com",
		"https://yummygirl.com/",
		"https://www.yummygirl.com",
		"https://yummygirl.com/models/sofie-marie.html",
		"https://yummygirl.com/categories/movies_1_d.html",
	}
	no := []string{
		"https://example.com",
		"https://notyummygirl.com",
	}
	for _, u := range yes {
		if !s.MatchesURL(u) {
			t.Errorf("expected match for %s", u)
		}
	}
	for _, u := range no {
		if s.MatchesURL(u) {
			t.Errorf("unexpected match for %s", u)
		}
	}
}

func TestClassifyURL(t *testing.T) {
	cases := []struct {
		url  string
		want urlKind
	}{
		{"https://yummygirl.com", kindUpdates},
		{"https://yummygirl.com/", kindUpdates},
		{"https://yummygirl.com/categories/movies_1_d.html", kindUpdates},
		{"https://yummygirl.com/models/sofie-marie.html", kindModel},
		{"https://yummygirl.com/models/models.html", kindUpdates},
	}
	for _, tc := range cases {
		got := classifyURL(tc.url)
		if got != tc.want {
			t.Errorf("classifyURL(%q) = %d, want %d", tc.url, got, tc.want)
		}
	}
}

const testListingHTML = `<html><body>
<div class="updateItem">
	<a href="https://yummygirl.com/updates/My-First-Scene.html">
		<img src0_1x="/content/channel/MyFirstScene/1.jpg" />
	</a>
	<div class="updateDetails">
		<div class="cart_buttons cart_setid_18"></div>
		<h4>
			<a href="https://yummygirl.com/updates/My-First-Scene.html">My First Scene &amp; More</a>
		</h4>
		<p>
	<span class="tour_update_models">
			<a href="https://yummygirl.com/models/sofie-marie.html">Sofie Marie</a> , <a href="https://yummygirl.com/models/johnny.html">Johnny</a>
	</span>
 <span>04/15/2026</span>
		</p>
	</div>
</div>
<div class="updateItem">
	<a href="https://yummygirl.com/updates/Second-Scene-Test.html">
		<img src0_1x="/content/channel/SecondScene/1.jpg" />
	</a>
	<div class="updateDetails">
		<div class="cart_buttons cart_setid_18"></div>
		<h4>
			<a href="https://yummygirl.com/updates/Second-Scene-Test.html">Second Scene Test</a>
		</h4>
		<p>
	<span class="tour_update_models">
			<a href="https://yummygirl.com/models/oliver.html">Oliver Faze</a>
	</span>
 <span>04/10/2026</span>
		</p>
	</div>
</div>
<div class="global_pagination">
<a class="active" href="movies_1_d.html">1</a>
<a href="movies_2_d.html">2</a>
<a href="movies_50_d.html">50</a>
</div>
</body></html>`

func TestParseListingCards(t *testing.T) {
	scenes := parseListingCards([]byte(testListingHTML))
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	ps := scenes[0]
	if ps.id != "My-First-Scene" {
		t.Errorf("id = %q, want My-First-Scene", ps.id)
	}
	if ps.title != "My First Scene & More" {
		t.Errorf("title = %q", ps.title)
	}
	if ps.thumbnail != "/content/channel/MyFirstScene/1.jpg" {
		t.Errorf("thumbnail = %q", ps.thumbnail)
	}
	if len(ps.performers) != 2 || ps.performers[0] != "Sofie Marie" || ps.performers[1] != "Johnny" {
		t.Errorf("performers = %v", ps.performers)
	}
	if ps.date != "04/15/2026" {
		t.Errorf("date = %q", ps.date)
	}

	ps2 := scenes[1]
	if ps2.id != "Second-Scene-Test" {
		t.Errorf("id = %q, want Second-Scene-Test", ps2.id)
	}
	if ps2.title != "Second Scene Test" {
		t.Errorf("title = %q", ps2.title)
	}
	if len(ps2.performers) != 1 || ps2.performers[0] != "Oliver Faze" {
		t.Errorf("performers = %v", ps2.performers)
	}
}

func TestParseListingCardsDedup(t *testing.T) {
	doubled := testListingHTML + testListingHTML
	scenes := parseListingCards([]byte(doubled))
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (deduped)", len(scenes))
	}
}

const testModelBlockHTML = `<html><body>
<div class="update_block">
<div class="update_table_left">
	<div class="update_block_info">
		<span class="update_title">Model Scene One</span>
	<span class="tour_update_models">
			<a href="https://yummygirl.com/models/sofie-marie.html">Sofie Marie</a>
	</span>
		<span class="availdate">03/20/2026<br />Available to Members Now</span>
		<span class="latest_update_description">Description of the scene here.</span>
		<span class="update_tags">
Tags:
	  <a href="https://yummygirl.com/categories/bigboobs.html">Big Boobs</a>
	  <a href="https://yummygirl.com/categories/brunette.html">Brunette</a>
		</span>
	</div>
</div>
<div class="update_table_right">
	<div class="update_image">
		<a href="https://yummygirl.com/updates/ModelSceneOne.html">
		<img src0_1x="/content/channel/ModelSceneOne/0.jpg" />
		</a>
	</div>
</div>
</div>
<div class="update_block">
<div class="update_table_left">
	<div class="update_block_info">
		<span class="update_title">Model Scene Two</span>
	<span class="tour_update_models">
			<a href="https://yummygirl.com/models/sofie-marie.html">Sofie Marie</a> , <a href="https://yummygirl.com/models/spike.html">Spike</a>
	</span>
		<span class="availdate">02/15/2026<br />Available to Members Now</span>
		<span class="latest_update_description">Another scene description.</span>
		<span class="update_tags">
Tags:
	  <a href="https://yummygirl.com/categories/anal.html">Anal</a>
		</span>
	</div>
</div>
<div class="update_table_right">
	<div class="update_image">
		<a href="https://yummygirl.com/updates/ModelSceneTwo.html">
		<img src0_1x="/content/channel/ModelSceneTwo/0.jpg" />
		</a>
	</div>
</div>
</div>
<div class="global_pagination">
<a class="active" href="sets.php?id=2">1</a>
<a href="sets.php?id=2&page=2">2</a>
<a href="sets.php?id=2&page=5">5</a>
</div>
</body></html>`

func TestParseModelBlocks(t *testing.T) {
	scenes := parseModelBlocks([]byte(testModelBlockHTML))
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	ps := scenes[0]
	if ps.id != "ModelSceneOne" {
		t.Errorf("id = %q, want ModelSceneOne", ps.id)
	}
	if ps.title != "Model Scene One" {
		t.Errorf("title = %q", ps.title)
	}
	if ps.thumbnail != "/content/channel/ModelSceneOne/0.jpg" {
		t.Errorf("thumbnail = %q", ps.thumbnail)
	}
	if len(ps.performers) != 1 || ps.performers[0] != "Sofie Marie" {
		t.Errorf("performers = %v", ps.performers)
	}
	if ps.date != "03/20/2026" {
		t.Errorf("date = %q", ps.date)
	}
	if ps.description != "Description of the scene here." {
		t.Errorf("description = %q", ps.description)
	}
	if len(ps.tags) != 2 || ps.tags[0] != "Big Boobs" || ps.tags[1] != "Brunette" {
		t.Errorf("tags = %v", ps.tags)
	}

	ps2 := scenes[1]
	if ps2.id != "ModelSceneTwo" {
		t.Errorf("id = %q", ps2.id)
	}
	if len(ps2.performers) != 2 {
		t.Errorf("performers = %v", ps2.performers)
	}
	if len(ps2.tags) != 1 || ps2.tags[0] != "Anal" {
		t.Errorf("tags = %v", ps2.tags)
	}
}

func TestEstimateTotal(t *testing.T) {
	body := []byte(`<div class="global_pagination">
<a class="active" href="movies_1_d.html">1</a>
<a href="movies_2_d.html">2</a>
<a href="movies_141_d.html">141</a>
</div>`)
	got := estimateTotal(body, 12)
	if got != 141*12 {
		t.Errorf("estimateTotal = %d, want %d", got, 141*12)
	}
}

func TestEstimateTotalNoPagination(t *testing.T) {
	body := []byte(`<html><body>no pagination here</body></html>`)
	got := estimateTotal(body, 5)
	if got != 5 {
		t.Errorf("estimateTotal = %d, want 5", got)
	}
}

func TestHasNextPage(t *testing.T) {
	body := []byte(`<div class="global_pagination">
<a class="active" href="movies_1_d.html">1</a>
<a href="movies_2_d.html">2</a>
<a href="movies_5_d.html">5</a>
</div>`)

	if !hasNextPage(body, 1) {
		t.Error("expected hasNextPage(1) = true")
	}
	if !hasNextPage(body, 4) {
		t.Error("expected hasNextPage(4) = true")
	}
	if hasNextPage(body, 5) {
		t.Error("expected hasNextPage(5) = false")
	}
}

func TestExtractModelPagination(t *testing.T) {
	body := []byte(`<div class="global_pagination">
<a class="active" href="sets.php?id=2">1</a>
<a href="sets.php?id=2&page=2">2</a>
<a href="sets.php?id=2&page=86">86</a>
</div>`)

	id, maxPage := extractModelPagination(body)
	if id != "2" {
		t.Errorf("modelID = %q, want 2", id)
	}
	if maxPage != 86 {
		t.Errorf("maxPage = %d, want 86", maxPage)
	}
}

func TestExtractModelPaginationNone(t *testing.T) {
	body := []byte(`<html><body>no pagination</body></html>`)
	id, maxPage := extractModelPagination(body)
	if id != "" || maxPage != 0 {
		t.Errorf("expected empty id and 0 max, got %q, %d", id, maxPage)
	}
}

func TestToScene(t *testing.T) {
	ps := parsedScene{
		id:          "My-First-Scene",
		title:       "My First Scene",
		relURL:      "/updates/My-First-Scene.html",
		thumbnail:   "/content/channel/MyFirstScene/1.jpg",
		performers:  []string{"Sofie Marie"},
		date:        "04/15/2026",
		description: "A description",
		tags:        []string{"Big Boobs"},
	}
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	scene := toScene(ps, "https://yummygirl.com", "https://yummygirl.com/", now)

	if scene.ID != "My-First-Scene" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "yummygirl" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.URL != "https://yummygirl.com/updates/My-First-Scene.html" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Thumbnail != "https://yummygirl.com/content/channel/MyFirstScene/1.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if scene.Studio != "YummyGirl" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if scene.Description != "A description" {
		t.Errorf("Description = %q", scene.Description)
	}
	if len(scene.Tags) != 1 || scene.Tags[0] != "Big Boobs" {
		t.Errorf("Tags = %v", scene.Tags)
	}
	expected := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(expected) {
		t.Errorf("Date = %v, want %v", scene.Date, expected)
	}
}

func TestParseDate(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"04/15/2026", "2026-04-15"},
		{"12/31/2025", "2025-12-31"},
		{"", "0001-01-01"},
		{"not-a-date", "0001-01-01"},
	}
	for _, tc := range cases {
		got := parseDate(tc.in)
		if got.Format("2006-01-02") != tc.want {
			t.Errorf("parseDate(%q) = %s, want %s", tc.in, got.Format("2006-01-02"), tc.want)
		}
	}
}

func makeListingPage(ids []string, maxPage int) string {
	var b strings.Builder
	b.WriteString("<html><body>")
	for _, id := range ids {
		fmt.Fprintf(&b, `<div class="updateItem">
	<a href="/updates/%s.html">
		<img src0_1x="/thumb/%s.jpg" />
	</a>
	<div class="updateDetails">
		<h4><a href="/updates/%s.html">Scene %s</a></h4>
		<p>
	<span class="tour_update_models">
			<a href="/models/model.html">Model</a>
	</span>
 <span>04/20/2026</span>
		</p>
	</div>
</div>`, id, id, id, id)
	}
	if maxPage > 1 {
		b.WriteString(`<div class="global_pagination">`)
		for p := 1; p <= maxPage; p++ {
			fmt.Fprintf(&b, `<a href="movies_%d_d.html">%d</a>`, p, p)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString("</body></html>")
	return b.String()
}

func TestPaginatedScrape(t *testing.T) {
	page1 := makeListingPage([]string{"scene-a", "scene-b", "scene-c"}, 2)
	page2 := makeListingPage([]string{"scene-d", "scene-e"}, 2)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "movies_2"):
			_, _ = fmt.Fprint(w, page2)
		default:
			_, _ = fmt.Fprint(w, page1)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 5 {
		t.Fatalf("got %d scenes, want 5", len(got))
	}
	if got[0].Title != "Scene scene-a" {
		t.Errorf("title = %q", got[0].Title)
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	page := makeListingPage([]string{"s1", "s2", "s3", "s4"}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, page)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"s3": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, stopped := testutil.CollectScenesWithStop(t, ch)
	if len(got) != 3 {
		t.Fatalf("got %d scenes, want 3 — the page is finished before the stop", len(got))
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
}

func makeModelPage(ids []string, modelID string, maxPage int) string {
	var b strings.Builder
	b.WriteString("<html><body>")
	for _, id := range ids {
		fmt.Fprintf(&b, `<div class="update_block">
<div class="update_table_left">
	<div class="update_block_info">
		<span class="update_title">Scene %s</span>
	<span class="tour_update_models">
			<a href="/models/model.html">Model</a>
	</span>
		<span class="availdate">01/01/2026<br />Available</span>
		<span class="latest_update_description">Desc for %s</span>
		<span class="update_tags">
Tags:
	  <a href="/categories/tag.html">TestTag</a>
		</span>
	</div>
</div>
<div class="update_table_right">
	<a href="/updates/%s.html">
	<img src0_1x="/thumb/%s.jpg" />
	</a>
</div>
</div>`, id, id, id, id)
	}
	if maxPage > 1 {
		b.WriteString(`<div class="global_pagination">`)
		for p := 1; p <= maxPage; p++ {
			if p == 1 {
				fmt.Fprintf(&b, `<a href="sets.php?id=%s">1</a>`, modelID)
			} else {
				fmt.Fprintf(&b, `<a href="sets.php?id=%s&page=%d">%d</a>`, modelID, p, p)
			}
		}
		b.WriteString(`</div>`)
	}
	b.WriteString("</body></html>")
	return b.String()
}

func TestModelScrape(t *testing.T) {
	modelPage := makeModelPage([]string{"m1", "m2"}, "7", 2)
	modelPage2 := makeModelPage([]string{"m3"}, "7", 2)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "page=2") {
			_, _ = fmt.Fprint(w, modelPage2)
		} else {
			_, _ = fmt.Fprint(w, modelPage)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/models/test-model.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 3 {
		t.Fatalf("got %d scenes, want 3", len(got))
	}
	if got[0].Description != "Desc for m1" {
		t.Errorf("description = %q", got[0].Description)
	}
	if len(got[0].Tags) != 1 || got[0].Tags[0] != "TestTag" {
		t.Errorf("tags = %v", got[0].Tags)
	}
}

func TestModelSinglePage(t *testing.T) {
	modelPage := makeModelPage([]string{"only1"}, "", 0)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, modelPage)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/models/test-model.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 1 {
		t.Fatalf("got %d scenes, want 1", len(got))
	}
}
