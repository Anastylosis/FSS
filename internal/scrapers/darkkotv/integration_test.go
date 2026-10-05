//go:build integration

package darkkotv

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, latestupdateutil.New(site), "https://darkkotv.com/categories/movies.html", 3)
}

func TestLiveModelPage(t *testing.T) {
	testutil.RunLiveScrape(t, latestupdateutil.New(site), "https://darkkotv.com/models/alexis-tae.html", 3)
}
