//go:build integration

package milu

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveMilu(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://www.milu-av.com/", 3)
}

func TestLiveMiluVR(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://www.milu-av.com/?cat=48", 3)
}
