//go:build integration

package thehabibshow

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLive(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://thehabibshow.com", 3)
}

func TestLiveChannel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://thehabibshow.com/tour/channels/18/asian-porn/", 3)
}
