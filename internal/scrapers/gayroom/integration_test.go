//go:build integration

package gayroom

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/fycutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, fycutil.New(sites[7]), "https://manroyale.com/", 5)
}
