//go:build integration

package rawroadnation

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveRawRoadNation(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.rawroadnation.com/tour/categories/movies.html", 3)
}
