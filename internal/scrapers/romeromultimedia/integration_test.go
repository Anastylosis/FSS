//go:build integration

package romeromultimedia

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// One live smoke per parser variant the network exposes:
//   - plain WP REST listing (hentaied.com — the flagship)
//   - origin_website-filtered listing (Twinz, the lone site without its
//     own domain that lives on the hentaied.pro membership portal)

func TestLiveHentaied(t *testing.T) {
	cfg := findSite(t, "hentaied")
	testutil.RunLiveScrape(t, New(cfg), cfg.SiteBase+"/", 4)
}

func TestLiveTwinz(t *testing.T) {
	cfg := findSite(t, "twinz")
	testutil.RunLiveScrape(t, New(cfg), "https://hentaied.pro/projects/twinz/", 2)
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

func TestLiveAnatomikMedia(t *testing.T) {
	cfg := findSite(t, "anatomikmedia")
	testutil.RunLiveScrape(t, New(cfg), cfg.SiteBase+"/", 3)
}
