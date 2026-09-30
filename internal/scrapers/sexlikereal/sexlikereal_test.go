package sexlikereal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

const fixtureList = `{
  "data": [
    {
      "id": 81236,
      "title": "Church Babe Gone Wicked",
      "label": "church-babe-gone-wicked-81236",
      "description": "A great VR scene description.",
      "date": 1746403200,
      "fullVideoLength": 3563,
      "thumbnailUrl": "https://cdn-vr.sexlikereal.com/images/81236/cover.webp",
      "studio": {"id": 224, "name": "SLR Originals", "label": "slr-originals-224"},
      "actors": [
        {"id": 7099, "name": "Molly Little", "label": "molly-little-7099"},
        {"id": 5405, "name": "Nikki Nuttz", "label": "nikki-nuttz-5405"}
      ]
    },
    {
      "id": 80000,
      "title": "Second Scene Title",
      "label": "second-scene-title-80000",
      "description": "Another description.",
      "date": 1746316800,
      "fullVideoLength": 1200,
      "thumbnailUrl": "https://cdn-vr.sexlikereal.com/images/80000/cover.webp",
      "studio": {"id": 100, "name": "Other Studio", "label": "other-studio-100"},
      "actors": [
        {"id": 1234, "name": "Jane Doe", "label": "jane-doe-1234"}
      ]
    }
  ],
  "meta": {
    "pagination": {"page": 1, "perPage": 36, "totalCount": 55000, "totalPages": 1528}
  }
}`

const fixtureDetail81236 = `{
  "data": {
    "categories": [
      {"id": 191, "name": "Blow job", "label": "blow-job-vr"},
      {"id": 801, "name": "3D", "label": "3d-vr-porn"}
    ],
    "price": {"type": "individual", "discounted": false, "amount": 12.99, "gross": 12.99}
  }
}`

const fixtureDetail80000 = `{
  "data": {
    "categories": [
      {"id": 200, "name": "Cowgirl", "label": "cowgirl-vr"}
    ],
    "price": {"type": "bulk", "discounted": false, "amount": 6.99, "gross": 6.99}
  }
}`

const fixtureEmpty = `{"data": [], "meta": {"pagination": {"page": 2, "perPage": 36, "totalCount": 2, "totalPages": 1}}}`

func newTestServer(listPages map[int]string, details map[int]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v3/scenes" && r.URL.Query().Get("page") != "":
			page := 1
			if p := r.URL.Query().Get("page"); p != "" {
				if _, err := fmt.Sscanf(p, "%d", &page); err != nil {
					page = 1
				}
			}
			if body, ok := listPages[page]; ok {
				_, _ = fmt.Fprint(w, body)
				return
			}
			_, _ = fmt.Fprint(w, fixtureEmpty)
		case len(r.URL.Path) > len("/v3/scenes/"):
			idStr := r.URL.Path[len("/v3/scenes/"):]
			id := 0
			if _, err := fmt.Sscanf(idStr, "%d", &id); err == nil {
				if body, ok := details[id]; ok {
					_, _ = fmt.Fprint(w, body)
					return
				}
			}
			http.NotFound(w, r)
		default:
			_, _ = fmt.Fprint(w, fixtureEmpty)
		}
	}))
}

func collect(ch <-chan scraper.SceneResult) []scraper.SceneResult {
	var results []scraper.SceneResult
	for r := range ch {
		results = append(results, r)
	}
	return results
}

func TestResolveFilter(t *testing.T) {
	tests := []struct {
		url      string
		wantMode filterMode
		wantID   string
	}{
		{"https://www.sexlikereal.com/scenes", filterAll, ""},
		{"https://www.sexlikereal.com", filterAll, ""},
		{"https://www.sexlikereal.com/studios/slr-originals-224", filterStudio, "224"},
		{"https://www.sexlikereal.com/pornstars/molly-little-7099", filterModel, "7099"},
	}
	for _, tt := range tests {
		mode, id := resolveFilter(tt.url)
		if mode != tt.wantMode {
			t.Errorf("resolveFilter(%q) mode = %d, want %d", tt.url, mode, tt.wantMode)
		}
		if id != tt.wantID {
			t.Errorf("resolveFilter(%q) id = %q, want %q", tt.url, id, tt.wantID)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.sexlikereal.com/scenes", true},
		{"https://sexlikereal.com/studios/slr-originals-224", true},
		{"https://www.sexlikereal.com", true},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer(
		map[int]string{1: fixtureList},
		map[int]string{81236: fixtureDetail81236, 80000: fixtureDetail80000},
	)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), apiBaseURL: ts.URL}
	ch, err := s.ListScenes(context.Background(), "https://www.sexlikereal.com/scenes", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(ch)
	var scenes int
	for _, r := range results {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.ID == "81236" {
				if r.Scene.Title != "Church Babe Gone Wicked" {
					t.Errorf("title = %q", r.Scene.Title)
				}
				if r.Scene.Studio != "SLR Originals" {
					t.Errorf("studio = %q", r.Scene.Studio)
				}
				if len(r.Scene.Performers) != 2 {
					t.Errorf("performers = %v", r.Scene.Performers)
				}
				if r.Scene.Duration != 3563 {
					t.Errorf("duration = %d, want 3563", r.Scene.Duration)
				}
				if len(r.Scene.Tags) != 2 || r.Scene.Tags[0] != "Blow job" {
					t.Errorf("tags = %v", r.Scene.Tags)
				}
				if r.Scene.URL != "https://www.sexlikereal.com/scenes/church-babe-gone-wicked-81236" {
					t.Errorf("url = %q", r.Scene.URL)
				}
				if r.Scene.LowestPrice != 12.99 {
					t.Errorf("lowestPrice = %f, want 12.99", r.Scene.LowestPrice)
				}
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := newTestServer(
		map[int]string{1: fixtureList},
		map[int]string{81236: fixtureDetail81236},
	)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), apiBaseURL: ts.URL}
	ch, err := s.ListScenes(context.Background(), "https://www.sexlikereal.com/scenes", scraper.ListOpts{
		KnownIDs: map[string]bool{"80000": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(ch)
	var scenes, stopped int
	for _, r := range results {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stopped++
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
}

func TestStudioFilter(t *testing.T) {
	var capturedStudios string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/scenes" {
			capturedStudios = r.URL.Query().Get("studios")
		}
		_, _ = fmt.Fprint(w, fixtureEmpty)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), apiBaseURL: ts.URL}
	ch, _ := s.ListScenes(context.Background(), "https://www.sexlikereal.com/studios/slr-originals-224", scraper.ListOpts{})
	collect(ch)

	if capturedStudios != "224" {
		t.Errorf("studios param = %q, want 224", capturedStudios)
	}
}

func TestPageFailureContinuesTraversal(t *testing.T) {
	const page1 = `{"data":[{"id":1,"title":"One","label":"one-1","fullVideoLength":100}],"meta":{"pagination":{"page":1,"perPage":36,"totalCount":3,"totalPages":3}}}`
	const page3 = `{"data":[{"id":3,"title":"Three","label":"three-3","fullVideoLength":100}],"meta":{"pagination":{"page":3,"perPage":36,"totalCount":3,"totalPages":3}}}`
	const detail = `{"data":{"categories":[],"price":{"amount":0}}}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/scenes" {
			switch r.URL.Query().Get("page") {
			case "1":
				_, _ = fmt.Fprint(w, page1)
			case "2":
				w.WriteHeader(http.StatusBadRequest) // non-retryable, fails fast
			case "3":
				_, _ = fmt.Fprint(w, page3)
			}
			return
		}
		_, _ = fmt.Fprint(w, detail)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), apiBaseURL: ts.URL}
	ch, err := s.ListScenes(context.Background(), "https://www.sexlikereal.com/scenes", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(ch)
	var scenes, errs int
	ids := map[string]bool{}
	for _, r := range results {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			ids[r.Scene.ID] = true
		case scraper.KindError:
			errs++
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2 (page 2 failure should not abort the rest of the traversal)", scenes)
	}
	if !ids["1"] || !ids["3"] {
		t.Errorf("expected scenes 1 and 3, got %v", ids)
	}
	if errs == 0 {
		t.Errorf("expected an error result for the failed page")
	}
}

func TestModelFilter(t *testing.T) {
	var capturedModels string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/scenes" {
			capturedModels = r.URL.Query().Get("models")
		}
		_, _ = fmt.Fprint(w, fixtureEmpty)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), apiBaseURL: ts.URL}
	ch, _ := s.ListScenes(context.Background(), "https://www.sexlikereal.com/pornstars/molly-little-7099", scraper.ListOpts{})
	collect(ch)

	if capturedModels != "7099" {
		t.Errorf("models param = %q, want 7099", capturedModels)
	}
}

// multiPageServer serves totalPages pages of one scene each, recording the
// order pages were requested in and the peak number of concurrent list
// requests. gate, when non-nil, is invoked inside the list handler so a test
// can hold requests open and observe overlap.
type multiPageServer struct {
	*httptest.Server
	mu         sync.Mutex
	order      []int
	inFlight   int
	peakFlight int
}

func newMultiPageServer(totalPages int, gate func(page int)) *multiPageServer {
	m := &multiPageServer{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/scenes" {
			_, _ = fmt.Fprint(w, `{"data":{"categories":[],"price":{"amount":0}}}`)
			return
		}
		page := 1
		_, _ = fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)

		m.mu.Lock()
		m.order = append(m.order, page)
		m.inFlight++
		if m.inFlight > m.peakFlight {
			m.peakFlight = m.inFlight
		}
		m.mu.Unlock()

		if gate != nil {
			gate(page)
		}

		m.mu.Lock()
		m.inFlight--
		m.mu.Unlock()

		_, _ = fmt.Fprintf(w, `{"data":[{"id":%d,"title":"S%d","label":"s-%d","fullVideoLength":10}],`+
			`"meta":{"pagination":{"page":%d,"perPage":36,"totalCount":%d,"totalPages":%d}}}`,
			page, page, page, page, totalPages, totalPages)
	}))
	return m
}

func (m *multiPageServer) snapshot() ([]int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int(nil), m.order...), m.peakFlight
}

// TestFullTraversalFetchesEveryPage guards the parallel walk: a full traversal
// must still reach every page exactly once, order notwithstanding.
func TestFullTraversalFetchesEveryPage(t *testing.T) {
	const totalPages = 25
	ts := newMultiPageServer(totalPages, nil)
	defer ts.Close()

	s := New()
	s.client = ts.Client()
	s.apiBaseURL = ts.URL

	ch, err := s.ListScenes(context.Background(), "https://www.sexlikereal.com/scenes",
		scraper.ListOpts{Workers: 8})
	if err != nil {
		t.Fatal(err)
	}

	ids := map[string]bool{}
	total := 0
	for _, r := range collect(ch) {
		switch r.Kind {
		case scraper.KindScene:
			if ids[r.Scene.ID] {
				t.Errorf("scene %s emitted twice", r.Scene.ID)
			}
			ids[r.Scene.ID] = true
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}

	if len(ids) != totalPages {
		t.Errorf("got %d scenes, want %d", len(ids), totalPages)
	}
	for p := 1; p <= totalPages; p++ {
		if !ids[strconv.Itoa(p)] {
			t.Errorf("missing scene from page %d", p)
		}
	}
	if total != totalPages {
		t.Errorf("total = %d, want %d", total, totalPages)
	}
}

// TestFullTraversalFetchesPagesConcurrently is the regression test for the
// 13-hour full scrape: the listing walk, not the detail fetch, is what bounds a
// --full/--refresh run, so pages after the first must overlap. Reverting to a
// serial walk drops peak concurrency to 1 and fails here.
func TestFullTraversalFetchesPagesConcurrently(t *testing.T) {
	const totalPages = 20
	const barrier = 4
	// Hold the fan-out pages until `barrier` of them have arrived, so the
	// assertion observes real overlap rather than whatever incidental overlap
	// unblocked requests happen to produce. Page 1 is deliberately exempt: the
	// parallel walk has to fetch it alone to learn totalPages, so counting it
	// here would make the barrier unreachable and leave the timeout as the only
	// exit. The timeout stays as the failure path — a serial walk never reaches
	// the barrier, so it releases after 3s with peak concurrency 1.
	released := make(chan struct{})
	var once sync.Once
	var arrived atomic.Int32

	ts := newMultiPageServer(totalPages, func(page int) {
		if page == 1 {
			return
		}
		if arrived.Add(1) >= barrier {
			once.Do(func() { close(released) })
		}
		select {
		case <-released:
		case <-time.After(3 * time.Second):
			once.Do(func() { close(released) })
		}
	})
	defer ts.Close()

	s := New()
	s.client = ts.Client()
	s.apiBaseURL = ts.URL

	ch, err := s.ListScenes(context.Background(), "https://www.sexlikereal.com/scenes",
		scraper.ListOpts{Workers: 8})
	if err != nil {
		t.Fatal(err)
	}
	collect(ch)

	_, peak := ts.snapshot()
	if peak < 2 {
		t.Errorf("peak concurrent list requests = %d, want >= 2 (page walk must not be serial)", peak)
	}
}

// TestIncrementalWalkStaysOrdered pins the other half of the strategy split:
// with KnownIDs set the early-stop only makes sense on an in-order walk, so
// pages must be requested sequentially and one at a time.
func TestIncrementalWalkStaysOrdered(t *testing.T) {
	ts := newMultiPageServer(10, nil)
	defer ts.Close()

	s := New()
	s.client = ts.Client()
	s.apiBaseURL = ts.URL

	ch, err := s.ListScenes(context.Background(), "https://www.sexlikereal.com/scenes",
		scraper.ListOpts{Workers: 8, KnownIDs: map[string]bool{"6": true}})
	if err != nil {
		t.Fatal(err)
	}

	stopped := 0
	for _, r := range collect(ch) {
		if r.Kind == scraper.KindStoppedEarly {
			stopped++
		}
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}

	order, peak := ts.snapshot()
	if peak != 1 {
		t.Errorf("peak concurrent list requests = %d, want 1 (incremental walk must stay serial)", peak)
	}
	want := []int{1, 2, 3, 4, 5, 6}
	if len(order) != len(want) {
		t.Fatalf("pages fetched = %v, want %v (must stop at the known ID)", order, want)
	}
	for i, p := range want {
		if order[i] != p {
			t.Fatalf("pages fetched = %v, want %v", order, want)
		}
	}
}

// TestPageWorkersCappedIndependentlyOfWorkers is the guard that protects the
// upstream origin. The stall rate on this API rises with concurrency (~17%
// serial vs 47% at 8-way), so the page pool must stay bounded by
// maxPageWorkers no matter how high --workers is set. An operator running
// `-w 500` must not produce 500 concurrent listing requests.
func TestPageWorkersCappedIndependentlyOfWorkers(t *testing.T) {
	const totalPages = 80
	// Hold each list request briefly so in-flight requests accumulate; without
	// this the server answers too fast for a raised cap to show up as overlap.
	ts := newMultiPageServer(totalPages, func(int) { time.Sleep(20 * time.Millisecond) })
	defer ts.Close()

	s := New()
	s.client = ts.Client()
	s.apiBaseURL = ts.URL

	ch, err := s.ListScenes(context.Background(), "https://www.sexlikereal.com/scenes",
		scraper.ListOpts{Workers: 500})
	if err != nil {
		t.Fatal(err)
	}
	collect(ch)

	if _, peak := ts.snapshot(); peak > maxPageWorkers {
		t.Errorf("peak concurrent list requests = %d, want <= %d (page pool must not scale with --workers)",
			peak, maxPageWorkers)
	}
}

// TestStalledPagesDoNotSerialise is the regression test for the 13-hour scrape.
// Upstream stalls a fraction of listing pages for ~30s; the bug was that a
// serial walk paid every one of those end to end, so they summed. Several
// stalled pages must now overlap rather than accumulate.
func TestStalledPagesDoNotSerialise(t *testing.T) {
	const totalPages, stallEvery = 24, 4 // 6 stalled pages
	const stall = 400 * time.Millisecond

	ts := newMultiPageServer(totalPages, func(page int) {
		if page%stallEvery == 0 {
			time.Sleep(stall)
		}
	})
	defer ts.Close()

	s := New()
	s.client = ts.Client()
	s.apiBaseURL = ts.URL

	start := time.Now()
	ch, err := s.ListScenes(context.Background(), "https://www.sexlikereal.com/scenes",
		scraper.ListOpts{Workers: 8})
	if err != nil {
		t.Fatal(err)
	}
	scenes := 0
	for _, r := range collect(ch) {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}
	elapsed := time.Since(start)

	if scenes != totalPages {
		t.Errorf("got %d scenes, want %d", scenes, totalPages)
	}
	// Serialised, the six stalls alone would cost 6*400ms = 2.4s; overlapped
	// across the pool they cost little more than one stall (~400-500ms). The
	// bound sits halfway between the two so neither a loaded CI machine nor a
	// serial regression lands near it.
	serial := time.Duration((totalPages/stallEvery)*int(stall/time.Millisecond)) * time.Millisecond
	if elapsed > serial/2 {
		t.Errorf("full walk took %s, want under %s (serialised cost would be %s) — stalls are accumulating",
			elapsed.Round(time.Millisecond), (serial / 2).Round(time.Millisecond), serial)
	}
}

// TestRequestTimeoutClearsGatewayCeiling pins the second half of the fix. The
// API's own gateway gives up at ~30s, and pages have been observed returning a
// valid response at 30.4s. A client timeout at or below 30s races that ceiling
// and discards responses that were about to arrive.
func TestRequestTimeoutClearsGatewayCeiling(t *testing.T) {
	const gatewayCeiling = 30 * time.Second
	if requestTimeout <= gatewayCeiling {
		t.Errorf("requestTimeout = %s, must exceed the upstream gateway ceiling of %s",
			requestTimeout, gatewayCeiling)
	}
	if got := New().client.Timeout; got != requestTimeout {
		t.Errorf("client timeout = %s, want %s", got, requestTimeout)
	}
}

// TestCancellationDuringParallelWalk covers the goroutine-teardown path the
// parallel walk introduced: a cancelled context must close the output channel
// promptly rather than deadlocking between the page producer, the page pool and
// the detail pool. Run under -race, this also catches leaked sends.
func TestCancellationDuringParallelWalk(t *testing.T) {
	ts := newMultiPageServer(5000, func(int) { time.Sleep(10 * time.Millisecond) })
	defer ts.Close()

	s := New()
	s.client = ts.Client()
	s.apiBaseURL = ts.URL

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, "https://www.sexlikereal.com/scenes", scraper.ListOpts{Workers: 8})
	if err != nil {
		t.Fatal(err)
	}

	// Drain a few results, then cancel mid-walk.
	got := 0
	for r := range ch {
		if r.Kind == scraper.KindScene {
			got++
			if got == 10 {
				cancel()
				break
			}
		}
	}

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range ch { //nolint:revive // draining until the scraper closes out
		}
	}()
	select {
	case <-drained:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("output channel not closed within 10s of cancellation — teardown deadlocked")
	}
	cancel()
}

// The listing response already carries title, date, cast, runtime, description
// and thumbnail; the detail adds only categories and the price. Dropping the
// scene when it failed traded a missing tag list for a missing scene, which an
// authoritative --full Save would then delete.
func TestDetailFailureKeepsTheListingScene(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/scenes" {
			_, _ = fmt.Fprint(w, `{"data":[{"id":7,"title":"Kept","label":"kept","fullVideoLength":10}],`+
				`"meta":{"pagination":{"page":1,"perPage":36,"totalCount":1,"totalPages":1}}}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	s := New()
	s.client = ts.Client()
	s.apiBaseURL = ts.URL

	ch, err := s.ListScenes(context.Background(), "https://www.sexlikereal.com/scenes", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	scenes, errs := 0, 0
	for _, r := range collect(ch) {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.ID != "7" || r.Scene.Title != "Kept" {
				t.Errorf("scene = %+v", r.Scene)
			}
		case scraper.KindError:
			errs++
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1 — the listing already named it", scenes)
	}
	if errs == 0 {
		t.Error("the detail failure must still be reported")
	}
}
