//go:build integration

package powershotz

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLivePowershotz(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://powershotz.com/", 3)
}

func TestLivePowershotzModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://powershotz.com/Abbey", 3)
}
