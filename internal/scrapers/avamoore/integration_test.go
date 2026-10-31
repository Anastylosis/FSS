//go:build integration

package avamoore

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveAvaMoore(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.ava-moore.com/videos", 3)
}
