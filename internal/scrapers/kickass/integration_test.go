//go:build integration

package kickass

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrapeGuest(t *testing.T) {
	testutil.RunLiveScrape(t, newFor("ultracuckolds"), "https://www.ultracuckolds.com/", 3)
}

func TestLiveScrapeTour(t *testing.T) {
	testutil.RunLiveScrape(t, newFor("cumeatingcuckolds"), "https://www.cumeatingcuckolds.com/", 3)
}
