//go:build integration

package mfcshare

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func liveSite(t *testing.T, id string) *Scraper {
	t.Helper()
	return New(siteByID(t, id))
}

func TestLiveKerriKing(t *testing.T) {
	testutil.RunLiveScrape(t, liveSite(t, "kerriking"), "https://share.myfreecams.com/KerriKing", 3)
}

func TestLiveExquisiteGoddess(t *testing.T) {
	testutil.RunLiveScrape(t, liveSite(t, "exquisitegoddess"), "https://share.myfreecams.com/GoddessOfPleasure", 3)
}

func TestLiveAlexCoal(t *testing.T) {
	testutil.RunLiveScrape(t, liveSite(t, "alexcoal"), "https://share.myfreecams.com/AlexxxCoal", 3)
}
