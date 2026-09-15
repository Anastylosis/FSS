//go:build integration

package mamacitaz

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://mamacitaz.com/videos.en.html", 5)
}

func TestLiveScrapeChannel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://chicasloca.com/", 3)
}
