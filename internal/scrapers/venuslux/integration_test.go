//go:build integration

package venuslux

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveVenusLux(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://venus-lux.com/categories/movies.html", 3)
}
