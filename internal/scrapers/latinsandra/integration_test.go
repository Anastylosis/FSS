//go:build integration

package latinsandra

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveLatinSandra(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.latinsandra.com/", 3)
}
