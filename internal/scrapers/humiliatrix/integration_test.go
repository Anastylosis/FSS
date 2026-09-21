//go:build integration

package humiliatrix

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://www.humiliatrix.com/", 3)
}
