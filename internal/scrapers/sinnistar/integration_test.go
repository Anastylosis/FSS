//go:build integration

package sinnistar

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://sinnistar.com", 3)
}

func TestLiveScrapeModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://sinnistar.com/cindy-amateur-anal.html", 3)
}
