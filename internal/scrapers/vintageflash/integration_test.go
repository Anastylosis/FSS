//go:build integration

package vintageflash

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// TestLiveVintageFlash skips while vintageflash.com redirects to nhlpcentral.com
// (folded in 2026-09; the catalogue is scraped by the nhlpcentral scraper). The
// probe skips only on that redirect, so any other failure still fails, and the
// test resumes if the tour ever comes back.
func TestLiveVintageFlash(t *testing.T) {
	skipIfFoldedIntoNHLP(t)
	testutil.RunLiveScrape(t, New(), "https://vintageflash.com", 3)
}

func skipIfFoldedIntoNHLP(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := New().checkHome(ctx); errors.Is(err, errFoldedIntoNHLP) {
		t.Skipf("vintageflash.com has been folded into nhlpcentral.com: %v", err)
	}
}
