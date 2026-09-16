//go:build integration

package futilestruggles

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.futilestruggles.com/trial/", 5)
}
