//go:build integration

package missfetilicious

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveMissFetilicious(t *testing.T) {
	const u = "https://fetiliciousfans.com"
	testutil.SkipIfPlaceholder(t, u)
	testutil.RunLiveScrape(t, New(), u, 3)
}
