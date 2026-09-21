//go:build integration

package trixvideo

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func liveScraper(t *testing.T, id string) *Scraper {
	t.Helper()
	return New(siteByID(t, id))
}

func TestLiveDallasDiamondz(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "dallasdiamondz"), "https://www.dallasdiamondz.com/tour/", 2)
}

func TestLiveDixiesTrailerPark(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "dixiestrailerpark"), "https://www.dixiestrailerpark.com/tour/", 2)
}

func TestLiveGrannyCumsHere(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "grannycumshere"), "https://www.grannycumshere.com/tour/", 2)
}

func TestLiveMsParisAndFriends(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "msparisandfriends"), "https://www.msparisandfriends.com/tour/", 2)
}

func TestLiveSuburbanTaboo(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "suburbantaboo"), "https://www.suburbantaboo.com/tour/", 2)
}

func TestLiveSwingingBiCouples(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "swingingbicouples"), "https://www.swingingbicouples.com/tour/", 2)
}

func TestLiveTampaHousewives(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "tampahousewives"), "https://www.tampahousewives.com/tour/", 2)
}

func TestLiveWhoreBaitHals(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "whorebaithals"), "https://www.whorebaithals.com/tour/", 2)
}

// A /models/ URL filters the same walk rather than parsing the model page, and
// a /categories/ URL walks a different path template. Both are separate modes.
func TestLiveDallasDiamondzModel(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "dallasdiamondz"),
		"https://www.dallasdiamondz.com/tour/models/DallasDiamondz.html", 2)
}

func TestLiveDallasDiamondzCategory(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "dallasdiamondz"),
		"https://www.dallasdiamondz.com/tour/categories/MILF.html", 2)
}

func TestLiveCherokeeDAss(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "cherokeedass"), "https://cherokeedass.com/tour/", 2)
}

func TestLiveDesperateAmateurs(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "desperateamateurs"), "https://desperateamateurs.com/tour3/", 2)
}

// The numbered tour has to carry through model and category modes too.
func TestLiveDesperateAmateursModel(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "desperateamateurs"),
		"https://desperateamateurs.com/tour3/models/Bexx.html", 2)
}

func TestLiveDesperateAmateursCategory(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "desperateamateurs"),
		"https://desperateamateurs.com/tour3/categories/BBW.html", 2)
}
