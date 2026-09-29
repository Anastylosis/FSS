//go:build integration

package sensationalvideo

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/masutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// siteByID looks a config up by name. Indexing the table positionally means a
// row inserted above silently points the test at a different site — which is
// exactly what happened in the Aylo tests.
func siteByID(t *testing.T, id string) masutil.SiteConfig {
	t.Helper()
	for _, cfg := range sites {
		if cfg.SiteID == id {
			return cfg
		}
	}
	t.Fatalf("no site %q in the table", id)
	return masutil.SiteConfig{}
}

func TestLiveScrape(t *testing.T) {
	cfg := siteByID(t, "plumperpass")
	testutil.RunLiveScrape(t, masutil.New(cfg), cfg.Base, 5)
}

func TestLiveScrapeBBWsGoneBlack(t *testing.T) {
	cfg := siteByID(t, "bbwsgoneblack")
	testutil.RunLiveScrape(t, masutil.New(cfg), cfg.Base, 5)
}
