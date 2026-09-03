//go:build integration

package pkfstudios

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestLiveScrape(t *testing.T) {
	s, err := scraper.ForID("pkfstudios")
	if err != nil {
		t.Fatalf("ForID: %v", err)
	}
	testutil.RunLiveScrape(t, s, "https://www.pkfstudios.com/updates/", 3)
}
