//go:build integration

package ibicella

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveIbicella(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://ibicella.com/videos", 3)
}

func TestLiveIbicellaCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://ibicella.com/videos/joi-games", 3)
}
