//go:build integration

package kink

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

const liveStudioURL = "https://www.kink.com"

func TestLiveScrape(t *testing.T) {
	testutil.SkipIfPlaceholder(t, liveStudioURL)
	testutil.RunLiveScrape(t, New(), liveStudioURL, 5)
}

func TestLiveScrapeKinkMen(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.kinkmen.com", 5)
}

// Series mode reaches a shoot with nothing but its id — the series page lists
// no cards — so the shoot's JSON-LD is the only source for the title and the
// date. Without it every series scene stored an empty title and a zero date.
func TestLiveScrapeSeries(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.kink.com/series/the-training-of-o", 3)
}
