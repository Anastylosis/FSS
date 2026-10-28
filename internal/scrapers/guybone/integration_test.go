//go:build integration

package guybone

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveGuyBone(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://guybone.com/", 3)
}
