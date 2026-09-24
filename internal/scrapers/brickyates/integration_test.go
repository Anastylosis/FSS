//go:build integration

package brickyates

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.brickyates.com/tour/", 3)
}

func TestLiveScrapeCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.brickyates.com/tour/categories/Bareback/1/latest/", 3)
}

func TestLiveScrapeModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.brickyates.com/tour/models/Henry.html", 3)
}
