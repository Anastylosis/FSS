//go:build integration

package nextdooramateur

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveNextDoorAmateur(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[0]), "http://www.nextdooramateur.com/", 3)
}

func TestLiveAmateurCreampies(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[1]), "http://www.amateurcreampies.com/", 3)
}

func TestLiveCreampieEbony(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[2]), "http://www.creampieebony.com/", 3)
}

func TestLiveCreampieSquad(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[3]), "http://creampiesquad.com/", 3)
}

func TestLiveGirlsClimaxing(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[4]), "http://www.girlsclimaxing.com/", 3)
}

func TestLiveHotWivesAndGirlfriends(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[5]), "http://www.hotwivesandgirlfriends.com/", 3)
}

func TestLiveWestCoastGangbangs(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[6]), "http://www.westcoastgangbangs.com/", 3)
}
