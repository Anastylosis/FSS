//go:build integration

package vrspy

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://vrspy.com/", 5)
}

func TestLiveStarPage(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://vrspy.com/star/angel-youngs", 2)
}
