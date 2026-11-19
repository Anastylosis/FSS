//go:build integration

package rocksboys

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveRocksBoys(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.rocksboys.com/", 3)
}
