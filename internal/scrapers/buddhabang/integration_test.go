//go:build integration

package buddhabang

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://buddhabangxxx.com/", 3)
}

func TestLiveScrapeCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://buddhabangxxx.com/video-category/kally-xo/", 3)
}
