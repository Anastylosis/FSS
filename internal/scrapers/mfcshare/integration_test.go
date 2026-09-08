//go:build integration

package mfcshare

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveKerriKing(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[0]), "https://share.myfreecams.com/KerriKing", 3)
}

func TestLiveExquisiteGoddess(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[1]), "https://share.myfreecams.com/GoddessOfPleasure", 3)
}
