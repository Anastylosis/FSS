//go:build integration

package jonnomedia

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func live(t *testing.T, url string) {
	t.Helper()
	s, err := scraper.ForURL(url)
	if err != nil {
		t.Fatal(err)
	}
	testutil.RunLiveScrape(t, s, url, 3)
}

func TestLiveUKNakedMen(t *testing.T) { live(t, "https://www.uknakedmen.com/tour/") }

func TestLiveUKNakedMenShowPage(t *testing.T) {
	live(t, "https://www.uknakedmen.com/tour/show.php?a=669_2")
}

func TestLiveUKHotJocks(t *testing.T) { live(t, "https://ukhotjocks.com/") }

func TestLiveButchDixon(t *testing.T) { live(t, "https://www.butchdixon.com/tour/") }

func TestLiveYoungBastards(t *testing.T) { live(t, "https://youngbastards.com/") }

func TestLiveYoungBastardsModel(t *testing.T) {
	live(t, "https://youngbastards.com/twink/alec-loob/35958")
}

func TestLiveSpritzz(t *testing.T) { live(t, "https://spritzz.com/") }

func TestLiveSpritzzModel(t *testing.T) {
	live(t, "https://spritzz.com/twink/rick-belour/32424")
}
