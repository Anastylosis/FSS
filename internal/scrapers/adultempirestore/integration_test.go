//go:build integration

package adultempirestore

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveForbiddenFruitsFilms(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[0]), "https://www.forbiddenfruitsfilms.com/", 3)
}

func TestLiveRodneyMoore(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[1]), "https://rodneymoorestore.com/", 3)
}

func TestLiveEdPowers(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[2]), "https://www.edpowersvod.com/", 3)
}

func TestLiveTransationalFantasies(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[2]), "https://www.transationalfantasies.com/", 3)
}

func TestLiveBlackTransWhackers(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[3]), "https://www.blacktranswhackers.com/", 3)
}

func TestLiveBrazilianTransGirls(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[4]), "https://www.braziliantransgirls.com/", 3)
}

func TestLiveBangMyTransAss(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[5]), "https://bangmytransass.com/", 3)
}
