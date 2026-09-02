//go:build integration

package ukxxxpass

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	for _, cfg := range sites {
		cfg := cfg
		t.Run(cfg.SiteID, func(t *testing.T) {
			testutil.RunLiveScrape(t, New(cfg), "https://"+cfg.Domain+"/", 3)
		})
	}
}

func TestLiveStudioListing(t *testing.T) {
	testutil.RunLiveScrape(t, newFor("splatbukkake"), "https://splatbukkake.xxx/movies/studio/1/splatbukkake", 3)
}
