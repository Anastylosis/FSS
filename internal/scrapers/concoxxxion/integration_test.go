//go:build integration

package concoxxxion

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveHornyHousehold(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://hornyhousehold.com/watch-newest-clips-and-scenes.html", 3)
}

func TestLiveConorCoxxx(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.conorcoxxx.com/conor-coxxx-clips.html", 3)
}

func TestLiveConorCoxxxHornyHouseholdChannel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.conorcoxxx.com/horny-household-clips.html", 3)
}

func TestLiveConorCoxxxMovies(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.conorcoxxx.com/watch-newest-concoxxxion-movies.html", 3)
}

func TestLiveConorCoxxxPerformer(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.conorcoxxx.com/655579/melanie-hicks-streaming-pornstar-videos.html", 3)
}

func TestLiveHornyHouseholdPerformer(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://hornyhousehold.com/scenes/655579/melanie-hicks-streaming-pornstar-videos.html", 3)
}
