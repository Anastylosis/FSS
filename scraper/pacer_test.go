package scraper

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPacerSpacesConcurrentCallers(t *testing.T) {
	const (
		delay   = 20 * time.Millisecond
		callers = 6
	)
	p := NewPacer(delay)

	var mu sync.Mutex
	var stamps []time.Time

	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !p.Wait(context.Background()) {
				t.Error("Wait reported a cancelled context")
				return
			}
			mu.Lock()
			stamps = append(stamps, time.Now())
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Six callers sharing one 20ms slot must span at least five slots. A pool
	// where each goroutine slept on its own finished the whole set at once.
	span := time.Since(start)
	if want := time.Duration(callers-1) * delay; span < want {
		t.Errorf("all %d callers finished in %v, want at least %v — they are not sharing a schedule",
			callers, span, want)
	}
	if len(stamps) != callers {
		t.Fatalf("got %d stamps, want %d", len(stamps), callers)
	}
}

func TestPacerZeroDelayNeverBlocks(t *testing.T) {
	p := NewPacer(0)
	start := time.Now()
	for i := 0; i < 100; i++ {
		if !p.Wait(context.Background()) {
			t.Fatal("Wait reported a cancelled context")
		}
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("100 waits on a zero delay took %v", elapsed)
	}
}

func TestPacerWaitHonoursCancellation(t *testing.T) {
	p := NewPacer(10 * time.Second)
	if !p.Wait(context.Background()) {
		t.Fatal("the first slot must be immediate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p.Wait(ctx) {
		t.Error("Wait must report false once ctx is done")
	}
}

// Pace falls back to a per-goroutine sleep when nothing installed a Pacer,
// which is what a scraper driven directly as a library sees.
func TestPaceWithoutAPacerSleepsLocally(t *testing.T) {
	start := time.Now()
	if !Pace(context.Background(), 30*time.Millisecond) {
		t.Fatal("Pace reported a cancelled context")
	}
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Errorf("Pace returned after %v, want the delay to be honoured", elapsed)
	}
}

func TestPaceUsesTheContextPacer(t *testing.T) {
	p := NewPacer(25 * time.Millisecond)
	ctx := WithPacer(context.Background(), p)
	if PacerFrom(ctx) != p {
		t.Fatal("PacerFrom did not return the installed Pacer")
	}

	// The passed delay is ignored in favour of the run's shared schedule, so a
	// caller asking for an hour still gets the Pacer's cadence.
	if !Pace(ctx, time.Hour) {
		t.Fatal("the first slot must be immediate")
	}
	start := time.Now()
	if !Pace(ctx, time.Hour) {
		t.Fatal("Pace reported a cancelled context")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("second slot took %v — the passed delay was used instead of the Pacer", elapsed)
	}
}

func TestPaceZeroDelayReturnsImmediately(t *testing.T) {
	start := time.Now()
	for i := 0; i < 50; i++ {
		if !Pace(context.Background(), 0) {
			t.Fatal("Pace reported a cancelled context")
		}
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("50 zero-delay paces took %v", elapsed)
	}
}
