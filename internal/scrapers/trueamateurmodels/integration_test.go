//go:build integration

package trueamateurmodels

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, elxupdateutil.New(site), "https://www.trueamateurmodels.com/previewtour/", 3)
}
