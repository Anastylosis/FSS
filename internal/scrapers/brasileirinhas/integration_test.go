//go:build integration

package brasileirinhas

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLive(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.brasileirinhas.com/", 3)
}

func TestLivePerformer(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.brasileirinhas.com/pornstar/elisa-sanches.html", 3)
}

func TestLiveCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.brasileirinhas.com/videos/anal.html", 3)
}
