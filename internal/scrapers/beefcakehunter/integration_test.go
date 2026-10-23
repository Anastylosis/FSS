//go:build integration

package beefcakehunter

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveBeefcakeHunter(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://beefcakehunter.com/home/", 3)
}
