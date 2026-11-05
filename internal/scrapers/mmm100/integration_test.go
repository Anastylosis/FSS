//go:build integration

package mmm100

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveMMM100(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://mmm100.com/", 3)
}
