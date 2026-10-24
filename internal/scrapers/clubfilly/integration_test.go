//go:build integration

package clubfilly

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveClubFilly(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.clubfilly.com/home.php", 3)
}
