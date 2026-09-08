//go:build integration

package maxing

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLive(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.maxing.jp/", 3)
}

func TestLiveActress(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.maxing.jp/shop/ac/ACT10364.html", 3)
}
