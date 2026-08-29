//go:build integration

package brokenlatinawhores

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, elxupdateutil.New(site), "https://brokenlatinawhores.com/", 3)
}
