//go:build integration

package closeup18

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveCloseUp18(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://18closeup.com/updates.php", 3)
}
