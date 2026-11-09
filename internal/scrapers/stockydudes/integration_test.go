//go:build integration

package stockydudes

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.stockydudes.com/scenes", 3)
}

func TestLiveCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.stockydudes.com/scenes/category/1-bareback", 3)
}
