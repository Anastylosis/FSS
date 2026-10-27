//go:build integration

package planetplus

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLivePlanetPlus(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://planetplus.jp/", 3)
}

func TestLivePlanetPlusLabel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://planetplus.jp/wp01/category/label/nacr/", 3)
}
