//go:build integration

package candidlooks

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveAmateurLapdancer(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[0]), "https://amateurlapdancer.com/", 3)
}

func TestLiveAmateurLapdancerCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[0]), "https://amateurlapdancer.com/content/category/membervideos/", 3)
}

func TestLiveOnlyFacials(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[1]), "https://onlyfacials.com/content/", 3)
}

func TestLiveGenuineFeatures(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[2]), "https://genuinefeatures.com/content/", 3)
}
