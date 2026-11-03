//go:build integration

package plushies

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLivePlushies(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://plushies.tv/tour/categories/movies.html", 3)
}
