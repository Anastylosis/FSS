//go:build integration

package materiall

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveMateriall(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://materiall.jp/", 3)
}
