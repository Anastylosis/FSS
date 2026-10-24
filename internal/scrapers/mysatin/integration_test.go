//go:build integration

package mysatin

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveMySatin(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[0]), "https://www.mysatin.com/", 2)
}

func TestLiveSatinPlay(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[1]), "https://www.satinplay.com/", 2)
}
