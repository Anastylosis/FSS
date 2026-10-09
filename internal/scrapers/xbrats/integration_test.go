//go:build integration

package xbrats

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, latestupdateutil.New(site), "https://xbrats.com/categories/movies.html", 3)
}

func TestLiveModelPage(t *testing.T) {
	testutil.RunLiveScrape(t, latestupdateutil.New(site), "https://xbrats.com/models/AloraJaymes.html", 3)
}
