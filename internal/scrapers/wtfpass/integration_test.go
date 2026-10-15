//go:build integration

package wtfpass

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// One live smoke for the parent (covers the whole network in one pass via
// per-card Series labels), one for a sister domain (own-catalogue path),
// and one for the alternate-domain config (hardfucktales.com paired with
// hardfuckgirls.com).

func TestLiveWTFPass(t *testing.T) {
	cfg := findSite(t, "wtfpass")
	skipIfOriginDown(t, cfg.SiteBase)
	testutil.RunLiveScrape(t, New(cfg), cfg.SiteBase+"/", 4)
}

func TestLiveTheArtPorn(t *testing.T) {
	cfg := findSite(t, "theartporn")
	skipIfOriginDown(t, cfg.SiteBase)
	testutil.RunLiveScrape(t, New(cfg), cfg.SiteBase+"/", 4)
}

func TestLiveHardFuckTales(t *testing.T) {
	cfg := findSite(t, "hardfucktales")
	skipIfOriginDown(t, cfg.SiteBase)
	testutil.RunLiveScrape(t, New(cfg), cfg.SiteBase+"/", 4)
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

// skipIfOriginDown skips when the network's shared origin answers its own home
// page with nginx's stock 404, as every one of its domains did on 2026-10-16.
// That is a site outage, not a URL the scraper built wrongly.
func skipIfOriginDown(t *testing.T, base string) {
	t.Helper()
	c := &http.Client{Timeout: 20 * time.Second}
	resp, err := c.Get(base + "/")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusNotFound && strings.Contains(string(body), "<center>nginx") {
		t.Skipf("%s serves nginx's default 404 on its home page; the origin is down", base)
	}
}
