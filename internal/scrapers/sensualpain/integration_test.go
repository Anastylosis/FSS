//go:build integration

package sensualpain

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://sensualpain.com", 3)
}

func TestLiveScrapeTracks(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://sensualpain.com/tracks.php", 3)
}
