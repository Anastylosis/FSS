//go:build integration

package fightingdolls

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveFightingDolls(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.fighting-dolls.com/all-fighting-dolls-videos/", 3)
}
