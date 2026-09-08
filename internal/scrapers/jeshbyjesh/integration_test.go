//go:build integration

package jeshbyjesh

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLive(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.jeshbyjesh.com/", 3)
}

func TestLiveSeries(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.jeshbyjesh.com/tour/series/season-1.html", 3)
}
