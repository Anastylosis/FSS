//go:build integration

package peatv

import (
	"context"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

const liveURL = "https://pea-tv.jp/search.php?b=1"

// TestLivePEATV skips while the site serves its closure notice (service ended
// 2026-09-01). The probe skips only on the notice itself, so any other empty
// listing still fails, and the test resumes if the site ever comes back.
func TestLivePEATV(t *testing.T) {
	skipIfServiceEnded(t)
	testutil.RunLiveScrape(t, New(), liveURL, 2)
}

func skipIfServiceEnded(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	body, err := New().fetchHTML(ctx, liveURL)
	if err != nil {
		return
	}
	if isShutdownNotice(body) {
		t.Skipf("pea-tv.jp ended its service on 2026-09-01; every page now serves only the closure notice (%q)", body)
	}
}
