//go:build integration

package hustler

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveHustler(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://hustlerunlimited.com/", 3)
}

func TestLiveHustlerModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://hustlerunlimited.com/model/dee-williams/", 2)
}

func TestLiveHustlerChannel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://hustlerunlimited.com/videos/?_sft_video_channels=barelylegal", 2)
}
