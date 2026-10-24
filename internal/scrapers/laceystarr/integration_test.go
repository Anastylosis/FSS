//go:build integration

package laceystarr

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveLaceyStarr(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.laceystarr.com", 3)
}

func TestLiveLaceyStarrModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.laceystarr.com/granny/model-lila-storm.php", 2)
}
