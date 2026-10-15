//go:build integration

package classmedia

import (
	"context"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestLiveSubspaceland(t *testing.T) {
	testutil.RunLiveScrape(t, NewSubspaceland(), "https://www.subspaceland.com", 3)
}

func TestLiveOldje(t *testing.T) {
	testutil.RunLiveScrape(t, NewOldje(), "https://www.oldje.com", 3)
}

// Oldje-3some publishes no title or cast for any set (checked 2026-10-16), so
// RunLiveScrape's title check cannot pass; this asserts what the site does
// publish instead.
func TestLiveOldje3some(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ch, err := NewOldje3some().ListScenes(ctx, "https://www.oldje-3some.com", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var got int
	for r := range ch {
		if r.Kind == scraper.KindError && ctx.Err() == nil {
			t.Errorf("scene error: %v", r.Err)
			continue
		}
		if r.Kind != scraper.KindScene {
			continue
		}
		sc := r.Scene
		if sc.ID == "" || sc.URL == "" || sc.Date.IsZero() || len(sc.Tags) == 0 {
			t.Errorf("scene %q is missing its id, URL, date or tags: %+v", sc.ID, sc)
		}
		if got++; got == 3 {
			cancel()
		}
	}
	if got == 0 {
		t.Fatal("no scenes")
	}
}
