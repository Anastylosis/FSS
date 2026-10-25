//go:build integration

package nanairo

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveNanairo(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://nanairo.co/ja/videos", 3)
}

func TestLiveNanairoSearch(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://nanairo.co/ja/search?text=Itsuki", 3)
}
