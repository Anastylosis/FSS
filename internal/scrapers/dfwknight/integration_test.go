//go:build integration

package dfwknight

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveDFWKnight(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://dfwknight.com/tour/video-store/", 3)
}
