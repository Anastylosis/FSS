//go:build integration

package studsvr

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.studsvr.com/scenes", 3)
}

func TestLiveScrapePerformer(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.studsvr.com/pornstar/casey-everett", 1)
}

func TestLiveScrapeCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.studsvr.com/category/bareback-porn-videos", 1)
}
