//go:build integration

package yezzclips

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.yezzclips.com/store_view.php?id=1054", 5)
}

func TestLiveScrapeSingleItem(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.yezzclips.com/store_view.php?id=2404&item=216986", 1)
}
