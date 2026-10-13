//go:build integration

package zexyvr

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://zexyvr.com/videos", 3)
}

func TestLiveScrapeTag(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://zexyvr.com/videos/22-29-vr-videos-118", 3)
}

func TestLiveScrapeModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://zexyvr.com/models/blossom/122", 3)
}
