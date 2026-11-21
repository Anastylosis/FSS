//go:build integration

package digitalark

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveDigitalArk(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://shop.digital-ark.com/", 3)
}

func TestLiveWeekender(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://shop.digital-ark.com/view/category/ct5", 3)
}
