//go:build integration

package diapergirls

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveDiaperGirls(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.diapergirls.com/", 3)
}
