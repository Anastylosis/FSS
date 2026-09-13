//go:build integration

package povperv

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/paysiteutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrapePOVPerv(t *testing.T) {
	testutil.RunLiveScrape(t, paysiteutil.New(sites[0]), "https://tour.povperv.com/scenes", 5)
}

func TestLiveScrapeNylonPerv(t *testing.T) {
	testutil.RunLiveScrape(t, paysiteutil.New(sites[1]), "https://nylonperv.com/videos", 5)
}
