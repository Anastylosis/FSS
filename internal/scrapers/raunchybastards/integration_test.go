//go:build integration

package raunchybastards

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.raunchybastards.com/", 3)
}

func TestLiveScrapeCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.raunchybastards.com/scenes/category/15-felching", 3)
}

func TestLiveScrapeProfile(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.raunchybastards.com/profile/1-clay", 3)
}
