//go:build integration

package realhotvr

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/adultdoorwayclassicutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, adultdoorwayclassicutil.New(site), "https://www.realhotvr.com/", 3)
}
