//go:build integration

package mindundermaster

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.mindundermaster.com/", 3)
}

func TestLiveScrapeChannel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.mindundermaster.com/channels/78/asmr/", 3)
}
