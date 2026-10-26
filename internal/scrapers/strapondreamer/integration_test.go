//go:build integration

package strapondreamer

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveStraponDreamer(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://strapondreamer.com/", 3)
}
