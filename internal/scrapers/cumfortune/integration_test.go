//go:build integration

package cumfortune

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveCumFortune(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://cumfortune.com/", 3)
}

func TestLiveCumFortuneModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://cumfortune.com/Promos/AAA_GIRLS/BELLA.htm", 3)
}
