//go:build integration

package salierixxx

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveSalieriXXX(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://salierixxx.com/en/", 3)
}
