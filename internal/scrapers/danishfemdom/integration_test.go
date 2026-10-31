//go:build integration

package danishfemdom

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveDanishFemdom(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.danishfemdom.com/en/trailers/", 3)
}
