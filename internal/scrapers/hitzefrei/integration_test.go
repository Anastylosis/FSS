//go:build integration

package hitzefrei

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://tour.hitzefrei.com/videos", 3)
}

func TestLiveScrapeBrand(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://tour.hitzefrei.com/videos?site=10", 3)
}

func TestLiveScrapeBrandHost(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://citycheck.hitzefrei.com/", 3)
}

func TestLiveScrapeModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://tour.hitzefrei.com/model/75/mini-hotcore", 3)
}
