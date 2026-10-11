//go:build integration

package oldnanny

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://oldnanny.com/en/tour2/scenes/all", 3)
}

func TestLiveScrapeAgedLove(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://oldnanny.com/en/tour2/scenes/site/AgedLove", 3)
}

func TestLiveScrapeAgedLoveDomain(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://www.agedlove.com/", 3)
}

func TestLiveScrapeSites(t *testing.T) {
	for _, site := range []string{"OldNanny", "OmaHotel", "OmaHunter", "UsaWives", "EuropeMature", "FrenzyBDSM", "LatinChili"} {
		t.Run(site, func(t *testing.T) {
			testutil.RunLiveScrape(t, New(), "https://oldnanny.com/en/tour2/scenes/site/"+site, 3)
		})
	}
}

func TestLiveScrapeModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://oldnanny.com/en/tour2/scenes/model/adriana-love", 3)
}

func TestLiveScrapeTag(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://oldnanny.com/en/tour2/scenes/tag/3/milf", 3)
}
