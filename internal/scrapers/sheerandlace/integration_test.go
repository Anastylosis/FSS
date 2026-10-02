//go:build integration

package sheerandlace

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://sheerandlace.com/tour/", 3)
}

func TestLiveScrapeSort(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://sheerandlace.com/tour/categories/movies/1/popular/", 3)
}

func TestLiveScrapeModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://sheerandlace.com/tour/models/melena-maria-rya.html", 3)
}
