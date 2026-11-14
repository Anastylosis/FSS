//go:build integration

package adultempirestore

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveForbiddenFruitsFilms(t *testing.T) {
	testutil.RunLiveScrape(t, siteFor(t, "https://www.forbiddenfruitsfilms.com/"), "https://www.forbiddenfruitsfilms.com/", 3)
}

func TestLiveRodneyMoore(t *testing.T) {
	testutil.RunLiveScrape(t, siteFor(t, "https://rodneymoorestore.com/"), "https://rodneymoorestore.com/", 3)
}

func TestLiveEdPowers(t *testing.T) {
	testutil.RunLiveScrape(t, siteFor(t, "https://www.edpowersvod.com/"), "https://www.edpowersvod.com/", 3)
}

func TestLiveTransationalFantasies(t *testing.T) {
	testutil.RunLiveScrape(t, siteFor(t, "https://www.transationalfantasies.com/"), "https://www.transationalfantasies.com/", 3)
}

func TestLiveBlackTransWhackers(t *testing.T) {
	testutil.RunLiveScrape(t, siteFor(t, "https://www.blacktranswhackers.com/"), "https://www.blacktranswhackers.com/", 3)
}

func TestLiveBrazilianTransGirls(t *testing.T) {
	testutil.RunLiveScrape(t, siteFor(t, "https://www.braziliantransgirls.com/"), "https://www.braziliantransgirls.com/", 3)
}

func TestLiveBangMyTransAss(t *testing.T) {
	testutil.RunLiveScrape(t, siteFor(t, "https://bangmytransass.com/"), "https://bangmytransass.com/", 3)
}

func siteFor(t *testing.T, u string) *Scraper {
	t.Helper()
	for _, c := range sites {
		if s := New(c); s.MatchesURL(u) {
			return s
		}
	}
	t.Fatalf("no site matches %s", u)
	return nil
}
