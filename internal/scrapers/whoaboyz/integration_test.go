//go:build integration

package whoaboyz

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveWhoaBoyz(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://whoaboyz.com/", 4)
}
