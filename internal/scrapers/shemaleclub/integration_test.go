//go:build integration

package shemaleclub

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveShemaleClub(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://shemale-club.com/", 2)
}
