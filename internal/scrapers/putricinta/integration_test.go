//go:build integration

package putricinta

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLivePutriCinta(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://putricinta.com/", 3)
}
