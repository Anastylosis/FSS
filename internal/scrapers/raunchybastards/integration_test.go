//go:build integration

package raunchybastards

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(Sites[0]), "https://www.raunchybastards.com/", 3)
}

func TestLiveScrapeCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(Sites[0]), "https://www.raunchybastards.com/scenes/category/15-felching", 3)
}

func TestLiveScrapeProfile(t *testing.T) {
	testutil.RunLiveScrape(t, New(Sites[0]), "https://www.raunchybastards.com/profile/1-clay", 3)
}

func TestLiveScrapeBoysHalfwayHouse(t *testing.T) {
	testutil.RunLiveScrape(t, New(Sites[1]), "https://www.boyshalfwayhouse.com/", 3)
}

func TestLiveScrapeBoysHalfwayHouseProfile(t *testing.T) {
	testutil.RunLiveScrape(t, New(Sites[1]), "https://www.boyshalfwayhouse.com/profile/313-kai", 3)
}
