//go:build integration

package dawnsplace

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveDawnsPlace(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.dawnsplace.com/", 3)
}
