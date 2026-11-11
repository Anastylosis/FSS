//go:build integration

package tiedvirgins

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveTiedVirgins(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://tiedvirgins.com/updates.html", 3)
}
