//go:build integration

package sadisticvillage

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveSadisticVillage(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://sadisticvill.com/", 3)
}
