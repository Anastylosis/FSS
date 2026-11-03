//go:build integration

package japanboyz

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveJapanboyz(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.japanboyz.com/categories/scenes.html", 3)
}
