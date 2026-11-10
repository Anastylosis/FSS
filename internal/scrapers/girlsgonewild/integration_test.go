//go:build integration

package girlsgonewild

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveGirlsGoneWild(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://girlsgonewild.com/new-videos", 3)
}

func TestLiveGirlsGoneWildStudio(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://girlsgonewild.com/studios/firstanalteens_studio", 3)
}

func TestLiveGirlsGoneWildModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://girlsgonewild.com/model/5251/olivia_westsun", 3)
}
