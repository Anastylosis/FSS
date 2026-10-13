//go:build integration

package riggsfilmsvip

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

const liveStudioURL = "https://riggsfilms.vip"

func TestLiveRiggsFilmsVIP(t *testing.T) {
	testutil.SkipIfPlaceholder(t, liveStudioURL)
	testutil.RunLiveScrape(t, New(), liveStudioURL, 3)
}
