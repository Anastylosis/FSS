//go:build integration

package cruelgirlfriend

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLive(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://cruelgf.com", 3)
}

func TestLiveGirlfriend(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://cruelgf.com/Girl.php?girlfriend=Zoe%20Grey", 3)
}
