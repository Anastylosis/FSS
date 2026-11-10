//go:build integration

package hunkch

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// One live test only: the site answers every path with 404 for minutes after a
// burst, so a second run straight after the first reads as an outage. The
// trance-video.com mapping is covered offline.
func TestLiveHunkCh(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.hunk-ch.com/search.php?b=234", 3)
}
