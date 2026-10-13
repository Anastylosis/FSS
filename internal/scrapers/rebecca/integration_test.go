//go:build integration

package rebecca

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLive(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://rebecca-web.com/", 3)
}

func TestLiveLabel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://rebecca-web.com/search.php?label_id=79", 3)
}

func TestLiveSeries(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://rebecca-web.com/search.php?series_id=1", 3)
}

func TestLiveActor(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://rebecca-web.com/actor.php?id=3541", 3)
}
