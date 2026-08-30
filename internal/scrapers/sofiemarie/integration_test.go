//go:build integration

package sofiemarie

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, latestupdateutil.New(site), "https://sofiemariexxx.com/categories/movies.html", 3)
}

func TestLiveModelPage(t *testing.T) {
	testutil.RunLiveScrape(t, latestupdateutil.New(site), "https://sofiemariexxx.com/models/Sofie-Marie.html", 3)
}
