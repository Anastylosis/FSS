//go:build integration

package showybeauty

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveShowyBeauty(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://showybeauty.com/", 3)
}
