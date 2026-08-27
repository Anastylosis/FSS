//go:build integration

package kbproductions

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/paysiteutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveMelinaMay(t *testing.T) {
	testutil.RunLiveScrape(t, paysiteutil.New(sites[0]), "https://melina-may.com/videos", 3)
}

func TestLivePassionPOV(t *testing.T) {
	testutil.RunLiveScrape(t, paysiteutil.New(sites[1]), "https://passionpov.com/videos", 3)
}

func TestLiveVRAllure(t *testing.T) {
	t.Skip("vrallure.com migrated from Next.js to IndieBucks/YPP HTML — needs standalone scraper")
}

func TestLiveManPuppy(t *testing.T) {
	t.Skip("manpuppy.com migrated from Next.js to IndieBucks/YPP HTML — needs standalone scraper")
}

func TestLiveMilflicious(t *testing.T) {
	testutil.RunLiveScrape(t, paysiteutil.New(sites[5]), "https://milflicious.com/videos", 3)
}
