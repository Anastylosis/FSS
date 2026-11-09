//go:build integration

package baberotica

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveBaberoticaVR(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://baberoticavr.com", 3)
}

func TestLiveBaberotica(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://baberotica.com", 3)
}
