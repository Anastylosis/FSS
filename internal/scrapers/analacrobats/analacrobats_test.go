package analacrobats

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

var _ scraper.StudioScraper = (*Scraper)(nil)

const testCardHTML = `<div class="item-col col -video" data-video="https://media.analacrobats.com/videos/test.mp4">
  <div class="item-inner-col inner-col">
    <a href="https://www.analacrobats.com/video/test-scene-549.html" title="">
      <span class="image image-ar">
        <canvas></canvas>
        <div class="image-wrapp">
          <img src="lazy.jpg" data-src="//cdn.analacrobats.com/thumbs/test-thumb.jpg" class="lazyload" alt="Test Scene">
        </div>
      </span>
      <span class="item-name">Test Scene</span>
    </a>
    <div class="item-info">
      <div class="item-stats">
        <div class="item-models">
          <div class="label">Models:</div>
          <a title="Jane Doe" href="https://www.analacrobats.com/models/jane-doe-13.html"><span class="sub-label">Jane Doe</span></a>,  <a title="Alice Smith" href="https://www.analacrobats.com/models/alice-smith-18.html"><span class="sub-label">Alice Smith</span></a>
        </div>
        <div class="item-data">
          <div class="item-date">
            <span class="sub-label">Date: 28.03.2025</span>
          </div>
          <div class="item-time">
            <span class="sub-label">Time: 48:45</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</div>`

const testCardNoModelsHTML = `<div class="item-col col -video" data-video="https://media.analacrobats.com/videos/test2.mp4">
  <div class="item-inner-col inner-col">
    <a href="https://www.analacrobats.com/video/no-models-100.html" title="">
      <span class="image image-ar">
        <canvas></canvas>
        <div class="image-wrapp">
          <img src="lazy.jpg" data-src="//cdn.analacrobats.com/thumbs/no-models.jpg" class="lazyload" alt="No Models">
        </div>
      </span>
      <span class="item-name">No Models Scene</span>
    </a>
    <div class="item-info">
      <div class="item-stats">
        <div class="item-data">
          <div class="item-date">
            <span class="sub-label">Date: 15.01.2024</span>
          </div>
          <div class="item-time">
            <span class="sub-label">Time: 1:02:08</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</div>`

func TestParseListingPage(t *testing.T) {
	body := []byte(testCardHTML + testCardNoModelsHTML)
	scenes := parseListingPage(body)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s := scenes[0]
	if s.id != "549" {
		t.Errorf("id = %q, want 549", s.id)
	}
	if s.title != "Test Scene" {
		t.Errorf("title = %q, want %q", s.title, "Test Scene")
	}
	if s.date.Format("2006-01-02") != "2025-03-28" {
		t.Errorf("date = %v, want 2025-03-28", s.date)
	}
	if s.duration != 2925 {
		t.Errorf("duration = %d, want 2925", s.duration)
	}
	if len(s.performers) != 2 || s.performers[0] != "Jane Doe" || s.performers[1] != "Alice Smith" {
		t.Errorf("performers = %v, want [Jane Doe Alice Smith]", s.performers)
	}
	if s.thumb != "https://cdn.analacrobats.com/thumbs/test-thumb.jpg" {
		t.Errorf("thumb = %q", s.thumb)
	}

	s2 := scenes[1]
	if s2.id != "100" {
		t.Errorf("id = %q, want 100", s2.id)
	}
	if len(s2.performers) != 0 {
		t.Errorf("performers = %v, want empty", s2.performers)
	}
	if s2.duration != 3728 {
		t.Errorf("duration = %d, want 3728 (1:02:08)", s2.duration)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"48:45", 2925},
		{"1:02:08", 3728},
		{"5:00", 300},
		{"0:30", 30},
	}
	for _, tt := range tests {
		if got := parseutil.ParseDurationColon(tt.in); got != tt.want {
			t.Errorf("ParseDurationColon(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestEstimateTotal(t *testing.T) {
	body := []byte(`<a href="page7.html">7</a><a href="page3.html">3</a>`)
	if got := estimateTotal(body, 30); got != 210 {
		t.Errorf("estimateTotal = %d, want 210", got)
	}
}

func buildPage(ids []int, maxPage int) []byte {
	var sb string
	for _, id := range ids {
		sb += fmt.Sprintf(`<div class="item-col col -video" data-video="">
  <div class="item-inner-col inner-col">
    <a href="/video/scene-%d.html" title="">
      <span class="image image-ar"><canvas></canvas>
        <div class="image-wrapp">
          <img src="lazy.jpg" data-src="//cdn.test/thumb-%d.jpg" class="lazyload" alt="">
        </div>
      </span>
      <span class="item-name">Scene %d</span>
    </a>
    <div class="item-info">
      <div class="item-stats">
        <div class="item-data">
          <div class="item-date"><span class="sub-label">Date: 01.01.2025</span></div>
          <div class="item-time"><span class="sub-label">Time: 10:00</span></div>
        </div>
      </div>
    </div>
  </div>
</div>`, id, id, id)
	}
	pager := ""
	for p := 2; p <= maxPage; p++ {
		pager += fmt.Sprintf(`<a href="page%d.html">%d</a>`, p, p)
	}
	return []byte(pager + sb)
}

func newTestServer(pages [][]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/most-recent/":
			_, _ = w.Write(buildPage(pages[0], len(pages)))
		default:
			pageNum := 0
			_, _ = fmt.Sscanf(r.URL.Path, "/most-recent/page%d.html", &pageNum)
			idx := pageNum - 1
			if idx >= 0 && idx < len(pages) {
				_, _ = w.Write(buildPage(pages[idx], len(pages)))
			} else {
				_, _ = fmt.Fprint(w, `<div>empty</div>`)
			}
		}
	}))
}

func TestListScenes(t *testing.T) {
	ts := newTestServer([][]int{{100, 200}})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Delay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
	if results[0].Title != "Scene 100" {
		t.Errorf("title = %q, want Scene 100", results[0].Title)
	}
	if results[0].SiteID != siteID {
		t.Errorf("siteID = %q, want %q", results[0].SiteID, siteID)
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	ts := newTestServer([][]int{{1, 2, 3, 4}})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"3": true},
		Delay:    time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	results, stopped := testutil.CollectScenesWithStop(t, ch)
	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if len(results) != 3 {
		t.Fatalf("got %d scenes, want 3 — the page is finished before the stop", len(results))
	}
}

func TestListScenesModel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(buildPage([]int{10, 20, 30}, 1))
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	modelURL := ts.URL + "/models/jane-doe-13.html"
	ch, err := s.ListScenes(context.Background(), modelURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 3 {
		t.Fatalf("got %d scenes, want 3", len(results))
	}
	if results[0].StudioURL != modelURL {
		t.Errorf("StudioURL = %q, want %q", results[0].StudioURL, modelURL)
	}
}

func TestListScenesModelKnownIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(buildPage([]int{10, 20, 30}, 1))
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/models/jane-doe-13.html", scraper.ListOpts{
		KnownIDs: map[string]bool{"20": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2 (skip known ID 20)", len(results))
	}
}

func TestListScenesPagination(t *testing.T) {
	page1 := []int{1, 2, 3}
	page2 := []int{4, 5}

	ts := newTestServer([][]int{page1, page2})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Delay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 5 {
		t.Fatalf("got %d scenes, want 5", len(results))
	}
}
