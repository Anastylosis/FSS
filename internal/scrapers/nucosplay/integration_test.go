//go:build integration

package nucosplay

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLive(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://nucosplay.com/", 3)
}

func TestLivePerformer(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://nucosplay.com/pornstar/mary/", 3)
}
