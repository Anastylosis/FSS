//go:build integration

package chrismarxxx

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveChrisMarxxx(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.marxxxmodels.com/", 3)
}
