//go:build integration

package goonmuse

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, latestupdateutil.New(site), "https://goonmuse.com/categories/movies.html", 3)
}
