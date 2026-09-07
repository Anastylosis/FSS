//go:build integration

package indigowhite

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLive(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://indigowhitetv.com/", 3)
}

func TestLiveVideosCollection(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://indigowhitetv.com/videos", 3)
}
