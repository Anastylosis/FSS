//go:build integration

package expliciteart

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.explicite-art.com/visitor/", 3)
}

func TestLiveScrapeChannel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.explicite-art.com/visitor/channel/16/orgy/", 3)
}

func TestLiveScrapePornstar(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.explicite-art.com/visitor/pornstars/best-of-53.html", 3)
}
