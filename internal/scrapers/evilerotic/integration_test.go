//go:build integration

package evilerotic

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveEvilErotic(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://evilerotic.com/videos", 3)
}
