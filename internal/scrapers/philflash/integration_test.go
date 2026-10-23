//go:build integration

package philflash

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLivePhilFlash(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://teenkasia.com/", 3)
}

func TestLiveKariSweets(t *testing.T) {
	testutil.RunLiveScrape(t, NewKariSweets(), "https://karisweets.com/", 1)
}
