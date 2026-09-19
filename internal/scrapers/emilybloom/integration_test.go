//go:build integration

package emilybloom

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, elxupdateutil.New(site), "https://emilybloom.com/", 3)
}
