//go:build integration

package restrainedelegance

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.restrainedelegance.com/", 3)
}

func TestLiveScrapeUpdates(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.restrainedelegance.com/whatsnew.php", 3)
}

func TestLiveScrapeModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.restrainedelegance.com/modelbio.php?id=Ariel+Anderssen", 3)
}
