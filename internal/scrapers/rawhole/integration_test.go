//go:build integration

package rawhole

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLive(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.rawhole.com", 3)
}

func TestLiveCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.rawhole.com/bareback/free-videos.html", 3)
}
