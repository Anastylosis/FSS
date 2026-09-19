//go:build integration

package badpuppy

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/natscmsutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, natscmsutil.New(site), "https://www.badpuppy.com/", 5)
}
