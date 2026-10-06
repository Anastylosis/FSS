//go:build integration

package zlatexa

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveZlatexa(t *testing.T) {
	const u = "https://zlatexa.com"
	testutil.SkipIfPlaceholder(t, u)
	testutil.RunLiveScrape(t, New(), u, 3)
}
