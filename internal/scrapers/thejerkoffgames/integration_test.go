//go:build integration

package thejerkoffgames

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, latestupdateutil.New(site), "https://www.thejerkoffmembers.com/categories/movies.html", 3)
}

func TestLiveModelPage(t *testing.T) {
	testutil.RunLiveScrape(t, latestupdateutil.New(site), "https://www.thejerkoffmembers.com/models/NataliaForrest.html", 3)
}
