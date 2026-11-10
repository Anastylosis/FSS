//go:build integration

package clubstiletto

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveClubStiletto(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.clubstiletto.com/", 3)
}
