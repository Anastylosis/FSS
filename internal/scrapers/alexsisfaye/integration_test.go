//go:build integration

package alexsisfaye

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://alexsisfaye.com/", 3)
}

func TestLiveScrapeVideos(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://alexsisfaye.com/videos", 3)
}
