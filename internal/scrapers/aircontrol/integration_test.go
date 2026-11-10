//go:build integration

package aircontrol

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveAirControl(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.i-dol.tv/", 3)
}

func TestLiveAirControlLabel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.i-dol.tv/works/list/label/4/", 3)
}
