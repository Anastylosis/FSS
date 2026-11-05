//go:build integration

package dirtycinema

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveDirtyCinema(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[0]), "https://dirtycinema.com/", 3)
}

func TestLiveMilfUCKD(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[1]), "https://milfuckd.com/", 3)
}
