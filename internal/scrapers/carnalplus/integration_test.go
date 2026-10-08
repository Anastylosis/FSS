//go:build integration

package carnalplus

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// One live smoke per parser variant the network uses.
//
//   - VariantNATS: funsizeboys.com is the representative sister site —
//     same template as the other 12 NATS domains.
//   - VariantGrid: carnalplus.com (parent portal aggregating every brand)
//     + baptistboys (sub-path).
//   - VariantWordPress: growlboys.com.

func TestLiveFunsizeBoys(t *testing.T) {
	cfg := findSite(t, "funsizeboys")
	testutil.RunLiveScrape(t, New(cfg), cfg.SiteBase+"/", 4)
}

func TestLiveCarnalPlus(t *testing.T) {
	cfg := findSite(t, "carnalplus")
	testutil.RunLiveScrape(t, New(cfg), cfg.SiteBase+"/", 4)
}

func TestLiveBaptistBoys(t *testing.T) {
	cfg := findSite(t, "baptistboys")
	testutil.RunLiveScrape(t, New(cfg), "https://carnalplus.com/baptistboys/", 4)
}

func TestLiveGrowlBoys(t *testing.T) {
	cfg := findSite(t, "growlboys")
	testutil.RunLiveScrape(t, New(cfg), cfg.SiteBase+"/", 4)
}

func TestLiveJockpackBrands(t *testing.T) {
	for _, id := range []string{"cumdumpsluts", "hungfuckers", "jockbreeders", "rawfuckboys", "twinkloads", "boundtwinks", "dirtyboysociety"} {
		t.Run(id, func(t *testing.T) {
			cfg := findSite(t, id)
			testutil.RunLiveScrape(t, New(cfg), cfg.SiteBase+"/", 3)
		})
	}
}

func TestLiveJockpackPortal(t *testing.T) {
	cfg := findSite(t, "jockpack")
	testutil.RunLiveScrape(t, New(cfg), "https://jockpack.com/", 3)
	testutil.RunLiveScrape(t, New(cfg), "https://jockpack.com/videos?site=hungfuckers", 3)
}

func TestLiveJockpackPortalOnlyBrands(t *testing.T) {
	for _, id := range []string{"betabetapi", "studloads", "jasonsparkslive"} {
		t.Run(id, func(t *testing.T) {
			cfg := findSite(t, id)
			testutil.RunLiveScrape(t, New(cfg), "https://jockpack.com/videos?site="+id, 3)
		})
	}
}

func findSite(t *testing.T, id string) SiteConfig {
	t.Helper()
	for _, cfg := range sites {
		if cfg.ID == id {
			return cfg
		}
	}
	t.Fatalf("no site config registered with ID %q", id)
	return SiteConfig{}
}
