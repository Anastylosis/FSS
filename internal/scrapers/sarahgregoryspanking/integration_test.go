//go:build integration

package sarahgregoryspanking

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.sarahgregoryspanking.com/", 3)
}
