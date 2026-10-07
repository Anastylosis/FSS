package houseofyre

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

const listingHTML = `<html><body>
<div class="latestUpdateB" data-setid="100">
<div class="videoPic">
<a href="https://www.example.com/scenes/Scene-One_vids.html">
<video poster_2x="/content/contentthumbs/10/00/10000-2x.jpg" src="/content/contentthumbs/10/00/10000.mp4"></video></a>
</div>
<div class="latestUpdateBinfo">
<div class="vBuyButtons">
<a href="javascript:rent_buy_options('100', 0, 1)">
<div class="buttons_light buyProduct bpbtn bpbtn_nested" id="buy_button">Buy ($12.99)</div></a>
</div>
<h4 class="link_bright">
<a href="https://www.example.com/scenes/Scene-One_vids.html">Scene One Title</a>
</h4>
<p class="link_light">
<a class="link_bright infolink" href="https://www.example.com/models/PerformerA.html">Performer A</a>, <a class="link_bright infolink" href="https://www.example.com/models/PerformerB.html">Performer B</a>
</p>
<ul class="videoInfo">
<li class="text_med"><!-- Date -->03/15/2026 </li>
<li class="text_med"><i class="fas fa-video"></i>35 min</li>
</ul>
</div></div>

<div class="latestUpdateB" data-setid="200">
<div class="videoPic">
<a href="https://www.example.com/scenes/Scene-Two_vids.html">
<video poster_2x="/content/contentthumbs/20/00/20000-2x.jpg" src="/content/contentthumbs/20/00/20000.mp4"></video></a>
</div>
<div class="latestUpdateBinfo">
<div class="vBuyButtons">
<a href="javascript:rent_buy_options('200', 0, 1)">
<div class="buttons_light buyProduct bpbtn bpbtn_nested" id="buy_button">Buy ($17.99)</div></a>
</div>
<h4 class="link_bright">
<a href="https://www.example.com/scenes/Scene-Two_vids.html">Scene Two Title</a>
</h4>
<p class="link_light">
<a class="link_bright infolink" href="https://www.example.com/models/PerformerC.html">Performer C</a>
</p>
<ul class="videoInfo">
<li class="text_med"><!-- Date -->02/10/2026 </li>
<li class="text_med"><i class="fas fa-video"></i>44 min</li>
</ul>
</div></div>

<div class="pagination">
<a class="active border_btn radius" href="https://www.example.com/categories/movies.html">1</a>
<a class="border_btn radius" href="https://www.example.com/categories/movies_2.html">2</a>
<a class="border_btn radius" href="https://www.example.com/categories/movies_3.html">3</a>
</div>
</body></html>`

const detailHTML = `<html><head>
<meta name="description" content="Short truncated description..." />
<meta name="keywords" content="Tag1,Tag2,Performer A" />
</head><body>
<div class="vidImgContent text_light">
<p>Full scene description with all the details.</p>
</div>
<div class='blogTags'><ul><li><a class="border_btn" href="/categories/tag1.html"><i class="fas fa-tag text_med"></i>Tag One</a></li><li><a class="border_btn" href="/categories/tag2.html"><i class="fas fa-tag text_med"></i>Tag Two</a></li><li><a class="border_btn" href="/categories/tag3.html"><i class="fas fa-tag text_med"></i>Tag Three</a></li></ul></div>
</body></html>`

func TestParseListingPage(t *testing.T) {
	scenes := parseListingPage([]byte(listingHTML), "https://www.example.com")
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s := scenes[0]
	if s.id != "100" {
		t.Errorf("id = %q", s.id)
	}
	if s.title != "Scene One Title" {
		t.Errorf("title = %q", s.title)
	}
	if s.url != "https://www.example.com/scenes/Scene-One_vids.html" {
		t.Errorf("url = %q", s.url)
	}
	if len(s.performers) != 2 || s.performers[0] != "Performer A" || s.performers[1] != "Performer B" {
		t.Errorf("performers = %v", s.performers)
	}
	wantDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	if !s.date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", s.date, wantDate)
	}
	if s.duration != 35*60 {
		t.Errorf("duration = %d, want %d", s.duration, 35*60)
	}
	if s.thumb != "https://www.example.com/content/contentthumbs/10/00/10000-2x.jpg" {
		t.Errorf("thumb = %q", s.thumb)
	}
	if s.price != 12.99 {
		t.Errorf("price = %f, want 12.99", s.price)
	}

	s2 := scenes[1]
	if s2.id != "200" {
		t.Errorf("s2.id = %q", s2.id)
	}
	if len(s2.performers) != 1 || s2.performers[0] != "Performer C" {
		t.Errorf("s2.performers = %v", s2.performers)
	}
	if s2.price != 17.99 {
		t.Errorf("s2.price = %f", s2.price)
	}
}

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage([]byte(detailHTML))
	if d.description != "Full scene description with all the details." {
		t.Errorf("description = %q", d.description)
	}
	if len(d.tags) != 3 {
		t.Fatalf("tags = %v, want 3 tags", d.tags)
	}
	if d.tags[0] != "Tag One" || d.tags[1] != "Tag Two" || d.tags[2] != "Tag Three" {
		t.Errorf("tags = %v", d.tags)
	}
}

func TestExtractMaxPage(t *testing.T) {
	maxPage := extractMaxPage([]byte(listingHTML))
	if maxPage != 3 {
		t.Errorf("maxPage = %d, want 3", maxPage)
	}
}

func TestHasNextPage(t *testing.T) {
	if !hasNextPage([]byte(listingHTML), 1) {
		t.Error("should have next page from page 1")
	}
	if hasNextPage([]byte(listingHTML), 3) {
		t.Error("should not have next page from page 3")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.houseofyre.com", true},
		{"https://houseofyre.com/categories/movies.html", true},
		{"https://www.houseofyre.com/models/AshlynPeaks.html", true},
		{"https://example.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestRun(t *testing.T) {
	singlePageListing := strings.ReplaceAll(listingHTML, "\n<div class=\"pagination\">\n<a class=\"active border_btn radius\" href=\"https://www.example.com/categories/movies.html\">1</a>\n<a class=\"border_btn radius\" href=\"https://www.example.com/categories/movies_2.html\">2</a>\n<a class=\"border_btn radius\" href=\"https://www.example.com/categories/movies_3.html\">3</a>\n</div>", "")

	var tsURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/scenes/") {
			_, _ = fmt.Fprint(w, detailHTML)
			return
		}
		_, _ = fmt.Fprint(w, strings.ReplaceAll(singlePageListing, "https://www.example.com", tsURL))
	}))
	defer ts.Close()
	tsURL = ts.URL

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	out := make(chan scraper.SceneResult)
	studioURL := ts.URL + "/studio/"
	go s.run(context.Background(), studioURL, scraper.ListOpts{Workers: 2}, out)

	var scenes []string
	for r := range out {
		if r.Kind == scraper.KindTotal || r.Kind == scraper.KindStoppedEarly {
			continue
		}
		if r.Err != nil {
			t.Logf("error: %v", r.Err)
			continue
		}
		scenes = append(scenes, r.Scene.ID)
		if r.Scene.StudioURL != studioURL {
			t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, studioURL)
		}
		if r.Scene.Description != "Full scene description with all the details." {
			t.Errorf("scene %s description = %q", r.Scene.ID, r.Scene.Description)
		}
		if len(r.Scene.Tags) != 3 {
			t.Errorf("scene %s tags = %v", r.Scene.ID, r.Scene.Tags)
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(scenes), scenes)
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	singlePageListing := strings.ReplaceAll(listingHTML, "\n<div class=\"pagination\">\n<a class=\"active border_btn radius\" href=\"https://www.example.com/categories/movies.html\">1</a>\n<a class=\"border_btn radius\" href=\"https://www.example.com/categories/movies_2.html\">2</a>\n<a class=\"border_btn radius\" href=\"https://www.example.com/categories/movies_3.html\">3</a>\n</div>", "")

	var tsURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/scenes/") {
			_, _ = fmt.Fprint(w, detailHTML)
			return
		}
		_, _ = fmt.Fprint(w, strings.ReplaceAll(singlePageListing, "https://www.example.com", tsURL))
	}))
	defer ts.Close()
	tsURL = ts.URL

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	out := make(chan scraper.SceneResult)
	go s.run(context.Background(), ts.URL, scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"200": true},
	}, out)

	var ids []string
	var stoppedEarly bool
	for r := range out {
		if r.Total > 0 {
			continue
		}
		if r.Kind == scraper.KindStoppedEarly {
			stoppedEarly = true
			continue
		}
		if r.Err != nil {
			t.Logf("error: %v", r.Err)
			continue
		}
		ids = append(ids, r.Scene.ID)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
	if len(ids) != 1 || ids[0] != "100" {
		t.Errorf("got IDs %v, want [100]", ids)
	}
}

func TestModelURL(t *testing.T) {
	modelPage := strings.ReplaceAll(listingHTML, "\n<div class=\"pagination\">\n<a class=\"active border_btn radius\" href=\"https://www.example.com/categories/movies.html\">1</a>\n<a class=\"border_btn radius\" href=\"https://www.example.com/categories/movies_2.html\">2</a>\n<a class=\"border_btn radius\" href=\"https://www.example.com/categories/movies_3.html\">3</a>\n</div>", "")

	var tsURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/scenes/") {
			_, _ = fmt.Fprint(w, detailHTML)
			return
		}
		_, _ = fmt.Fprint(w, strings.ReplaceAll(modelPage, "https://www.example.com", tsURL))
	}))
	defer ts.Close()
	tsURL = ts.URL

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	modelURL := tsURL + "/models/PerformerA.html"
	out := make(chan scraper.SceneResult)
	go s.run(context.Background(), modelURL, scraper.ListOpts{Workers: 2}, out)

	var scenes []string
	var total int
	for r := range out {
		if r.Total > 0 {
			total = r.Total
			continue
		}
		if r.Kind == scraper.KindStoppedEarly {
			continue
		}
		if r.Err != nil {
			t.Logf("error: %v", r.Err)
			continue
		}
		scenes = append(scenes, r.Scene.ID)
		if r.Scene.StudioURL != modelURL {
			t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, modelURL)
		}
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(scenes), scenes)
	}
}

func TestScraperInterface(t *testing.T) {
	var _ scraper.StudioScraper = New()
}
