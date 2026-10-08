//go:build integration

package coedproductions

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/coedutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveNebraskaCoeds(t *testing.T) {
	testutil.RunLiveScrape(t, coedutil.New(sites[0]), "https://tour.nebraskacoeds.com/", 3)
}

func TestLiveAfterHoursExposed(t *testing.T) {
	testutil.RunLiveScrape(t, coedutil.New(sites[2]), "https://afterhoursexposed.com/", 3)
}

func TestLiveEuroCoeds(t *testing.T) {
	testutil.RunLiveScrape(t, coedutil.New(sites[3]), "https://eurocoeds.com/", 3)
}

func TestLiveSouthBeachCoeds(t *testing.T) {
	testutil.RunLiveScrape(t, coedutil.New(sites[4]), "https://southbeachcoeds.com/", 3)
}
