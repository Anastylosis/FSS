//go:build integration

package westcoastproductions

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveWestCoastProductions(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://westcoastproductions.com/", 3)
}

func TestLiveBlackMassiveCocks(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://blackmassivecocks.com/", 3)
}

func TestLiveStudioFilter(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://westcoastproductions.com/watch-newest-west-coast-productions-clips-and-scenes.html?studio=93426", 3)
}
