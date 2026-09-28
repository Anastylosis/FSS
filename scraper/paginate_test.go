package scraper

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

func collectResults(ch <-chan SceneResult) []SceneResult {
	var results []SceneResult
	for r := range ch {
		results = append(results, r)
	}
	return results
}

func scene(id string) models.Scene {
	return models.Scene{ID: id, SiteID: "test", ScrapedAt: time.Now().UTC()}
}

func TestPaginate_basic(t *testing.T) {
	out := make(chan SceneResult, 100)
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		switch page {
		case 1:
			return PageResult{
				Scenes: []models.Scene{scene("a"), scene("b")},
				Total:  4,
			}, nil
		case 2:
			return PageResult{
				Scenes: []models.Scene{scene("c"), scene("d")},
				Done:   true,
			}, nil
		default:
			t.Fatalf("unexpected page %d", page)
			return PageResult{}, nil
		}
	}

	Paginate(context.Background(), ListOpts{}, "test", out, fetchPage)
	close(out)

	results := collectResults(out)
	var scenes []string
	gotTotal := 0
	for _, r := range results {
		switch r.Kind {
		case KindScene:
			scenes = append(scenes, r.Scene.ID)
		case KindTotal:
			gotTotal = r.Total
		}
	}
	if gotTotal != 4 {
		t.Errorf("total = %d, want 4", gotTotal)
	}
	if len(scenes) != 4 {
		t.Errorf("got %d scenes, want 4: %v", len(scenes), scenes)
	}
}

func TestPaginate_emptyFirstPage(t *testing.T) {
	out := make(chan SceneResult, 100)
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		return PageResult{}, nil
	}

	Paginate(context.Background(), ListOpts{}, "test", out, fetchPage)
	close(out)

	results := collectResults(out)
	if len(results) != 0 {
		t.Errorf("expected no results, got %d", len(results))
	}
}

// A page that filtered all its items out (Continue set, no Scenes) must not
// end the traversal — later pages still have scenes.
func TestPaginate_continuePastEmptyPage(t *testing.T) {
	out := make(chan SceneResult, 100)
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		switch page {
		case 1:
			return PageResult{Scenes: []models.Scene{scene("a")}, Total: 3}, nil
		case 2:
			// Page had items (e.g. photo-only) that filtered to zero scenes.
			return PageResult{Continue: true}, nil
		case 3:
			return PageResult{Scenes: []models.Scene{scene("b")}, Done: true}, nil
		default:
			t.Fatalf("unexpected page %d", page)
			return PageResult{}, nil
		}
	}

	Paginate(context.Background(), ListOpts{}, "test", out, fetchPage)
	close(out)

	var scenes []string
	for _, r := range collectResults(out) {
		if r.Kind == KindScene {
			scenes = append(scenes, r.Scene.ID)
		}
	}
	if len(scenes) != 2 || scenes[0] != "a" || scenes[1] != "b" {
		t.Errorf("scenes = %v, want [a b] (page 2 filtered-empty must not stop)", scenes)
	}
}

// Continue with Done true stops the loop after the empty page.
func TestPaginate_continueDoneStops(t *testing.T) {
	out := make(chan SceneResult, 100)
	calls := 0
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		calls++
		if page == 1 {
			return PageResult{Scenes: []models.Scene{scene("a")}}, nil
		}
		return PageResult{Continue: true, Done: true}, nil
	}

	Paginate(context.Background(), ListOpts{}, "test", out, fetchPage)
	close(out)

	if calls != 2 {
		t.Errorf("fetched %d pages, want 2 (Done must stop)", calls)
	}
}

// A CMS that echoes the same page forever (ignores the page param) must be
// stopped by repeat-page detection instead of looping until the safety cap.
func TestPaginate_repeatPageStops(t *testing.T) {
	out := make(chan SceneResult, 100)
	calls := 0
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		calls++
		// Same two scenes on every page, regardless of page number.
		return PageResult{Scenes: []models.Scene{scene("x"), scene("y")}}, nil
	}

	Paginate(context.Background(), ListOpts{}, "test", out, fetchPage)
	close(out)

	if calls != 2 {
		t.Errorf("fetched %d pages, want 2 (page 2 echoes page 1 → stop)", calls)
	}
	var scenes []string
	for _, r := range collectResults(out) {
		if r.Kind == KindScene {
			scenes = append(scenes, r.Scene.ID)
		}
	}
	if len(scenes) != 2 {
		t.Errorf("emitted %d scenes, want 2 (only page 1): %v", len(scenes), scenes)
	}
}

// A listing that pins one scene at the top of every page but otherwise advances
// must NOT be mistaken for an echoed page — only a full repeat stops the loop.
func TestPaginate_pinnedFirstSceneDoesNotStop(t *testing.T) {
	out := make(chan SceneResult, 100)
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		switch page {
		case 1:
			return PageResult{Scenes: []models.Scene{scene("pin"), scene("a")}}, nil
		case 2:
			return PageResult{Scenes: []models.Scene{scene("pin"), scene("b")}}, nil
		case 3:
			return PageResult{Done: true, Scenes: []models.Scene{scene("pin"), scene("c")}}, nil
		default:
			t.Fatalf("unexpected page %d", page)
			return PageResult{}, nil
		}
	}

	Paginate(context.Background(), ListOpts{}, "test", out, fetchPage)
	close(out)

	var scenes []string
	for _, r := range collectResults(out) {
		if r.Kind == KindScene {
			scenes = append(scenes, r.Scene.ID)
		}
	}
	// All three pages processed (pinned "pin" repeats but the rest advances).
	if len(scenes) != 6 {
		t.Errorf("emitted %d scenes, want 6 across 3 pages: %v", len(scenes), scenes)
	}
}

func TestPaginate_knownIDsStopsEarly(t *testing.T) {
	out := make(chan SceneResult, 100)
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		return PageResult{
			Scenes: []models.Scene{scene("new1"), scene("known"), scene("new2")},
			Total:  10,
		}, nil
	}

	opts := ListOpts{KnownIDs: map[string]bool{"known": true}}
	Paginate(context.Background(), opts, "test", out, fetchPage)
	close(out)

	results := collectResults(out)
	var scenes []string
	stoppedEarly := false
	for _, r := range results {
		switch r.Kind {
		case KindScene:
			scenes = append(scenes, r.Scene.ID)
		case KindStoppedEarly:
			stoppedEarly = true
		}
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
	// The page in hand is already paid for: the stored scene is skipped, the
	// rest of the page is emitted, and the walk stops at the end of the page.
	if len(scenes) != 2 || scenes[0] != "new1" || scenes[1] != "new2" {
		t.Errorf("scenes = %v, want [new1 new2]", scenes)
	}
}

func TestPaginate_fetchError(t *testing.T) {
	out := make(chan SceneResult, 100)
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		return PageResult{}, fmt.Errorf("server error")
	}

	Paginate(context.Background(), ListOpts{}, "test", out, fetchPage)
	close(out)

	results := collectResults(out)
	if len(results) != 1 || results[0].Kind != KindError {
		t.Errorf("expected one error result, got %v", results)
	}
}

func TestPaginate_contextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := make(chan SceneResult, 100)
	called := false
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		called = true
		return PageResult{}, nil
	}

	Paginate(ctx, ListOpts{}, "test", out, fetchPage)
	close(out)

	if called {
		t.Error("fetchPage should not be called when context is already cancelled")
	}
}

func TestPaginate_delayRespected(t *testing.T) {
	out := make(chan SceneResult, 100)
	pages := 0
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		pages = page
		if page < 3 {
			return PageResult{Scenes: []models.Scene{scene(fmt.Sprintf("s%d", page))}}, nil
		}
		return PageResult{}, nil
	}

	start := time.Now()
	Paginate(context.Background(), ListOpts{Delay: 50 * time.Millisecond}, "test", out, fetchPage)
	close(out)
	elapsed := time.Since(start)

	if pages != 3 {
		t.Errorf("fetched %d pages, want 3", pages)
	}
	if elapsed < 90*time.Millisecond {
		t.Errorf("elapsed %v, expected >= 90ms (2 delays)", elapsed)
	}
}

func TestPaginate_noProgressWhenTotalZero(t *testing.T) {
	out := make(chan SceneResult, 100)
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		return PageResult{
			Scenes: []models.Scene{scene("a")},
			Total:  0,
			Done:   true,
		}, nil
	}

	Paginate(context.Background(), ListOpts{}, "test", out, fetchPage)
	close(out)

	for r := range out {
		if r.Kind == KindTotal {
			t.Error("should not send Progress when Total is 0")
		}
	}
}

func TestPaginate_progressSentOnce(t *testing.T) {
	out := make(chan SceneResult, 100)
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		if page <= 2 {
			return PageResult{Scenes: []models.Scene{scene(fmt.Sprintf("s%d", page))}, Total: 10}, nil
		}
		return PageResult{}, nil
	}

	Paginate(context.Background(), ListOpts{}, "test", out, fetchPage)
	close(out)

	totalCount := 0
	for r := range out {
		if r.Kind == KindTotal {
			totalCount++
		}
	}
	if totalCount != 1 {
		t.Errorf("Progress sent %d times, want 1", totalCount)
	}
}

// TestPaginate_contextCancelled covers a context that is already dead before the
// first fetch. This covers the case that actually leaks: cancellation arriving
// **mid-walk, while a send is blocked on a consumer that has stopped reading**.
//
// The distinction matters because the existing test uses a buffered channel, so
// its sends never block and the `select … case <-ctx.Done()` around each one is
// never exercised. Delete those selects and that test still passes.
//
// This is also the single highest-leverage cancellation test in the suite: 144
// scrapers delegate their whole paging walk to Paginate, so mid-flight
// cancellation is Paginate's responsibility rather than each scraper's. Proving
// it here covers all of them, which is why testutil.AssertCancellable was only
// ever wired into the handful of scrapers that page themselves.
func TestPaginate_cancelledMidWalkWithBlockedSend(t *testing.T) {
	// Unbuffered: every send blocks until a consumer reads, exactly as a real
	// scrape does when the caller stops consuming.
	out := make(chan SceneResult)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var pages atomic.Int32
	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		pages.Add(1)
		// Unique IDs per page so the repeat-page guard never ends the walk —
		// only the context may.
		return PageResult{Scenes: []models.Scene{
			{ID: fmt.Sprintf("p%d-a", page), SiteID: "test"},
			{ID: fmt.Sprintf("p%d-b", page), SiteID: "test"},
		}}, nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		Paginate(ctx, ListOpts{}, "test", out, fetchPage)
	}()

	// Read one scene so the walk is genuinely under way, then stop reading and
	// cancel. Paginate is now blocked trying to send the next scene.
	if _, ok := <-out; !ok {
		t.Fatal("Paginate closed the channel before producing a scene")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Paginate did not return within 5s of cancellation while a send was " +
			"blocked — the sends are not selecting on ctx.Done(), and every scraper " +
			"built on Paginate leaks a goroutine per cancelled scrape")
	}
	if pages.Load() == 0 {
		t.Error("fetchPage was never called; the walk never started, so nothing was proven")
	}
}

// The same property for the Progress send, which is a separate select and is
// emitted before any scene.
func TestPaginate_cancelledWhileProgressSendBlocked(t *testing.T) {
	out := make(chan SceneResult)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fetchPage := func(_ context.Context, page int) (PageResult, error) {
		return PageResult{
			Total:  1000,
			Scenes: []models.Scene{{ID: fmt.Sprintf("p%d", page), SiteID: "test"}},
		}, nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		Paginate(ctx, ListOpts{}, "test", out, fetchPage)
	}()

	// Do not read at all: Paginate blocks on the very first send, the Progress
	// one. Give it a moment to get there, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Paginate did not return within 5s of cancellation while the Progress " +
			"send was blocked")
	}
}

// And for the StoppedEarly send, reached via KnownIDs.
func TestPaginate_cancelledWhileStoppedEarlySendBlocked(t *testing.T) {
	out := make(chan SceneResult)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fetchPage := func(_ context.Context, _ int) (PageResult, error) {
		return PageResult{Scenes: []models.Scene{{ID: "known", SiteID: "test"}}}, nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		Paginate(ctx, ListOpts{KnownIDs: map[string]bool{"known": true}}, "test", out, fetchPage)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Paginate did not return within 5s of cancellation while the StoppedEarly " +
			"send was blocked")
	}
}
