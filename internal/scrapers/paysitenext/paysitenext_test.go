package paysitenext

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/paysiteutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

// Domain-keyed config table — see testutil.CheckSiteDomainTable.
func TestSiteTableIntegrity(t *testing.T) {
	rows := make([]testutil.DomainRow, 0, len(sites))
	for _, c := range sites {
		rows = append(rows, testutil.DomainRow{ID: c.SiteID, Domain: c.Domain, Studio: c.StudioName})
	}
	testutil.CheckSiteDomainTable(t, rows)
}

func TestEveryURLResolvesToItsOwnScraper(t *testing.T) {
	for _, cfg := range sites {
		got, err := scraper.ForURL("https://" + cfg.Domain + "/")
		if err != nil {
			t.Errorf("ForURL(%s): %v", cfg.Domain, err)
			continue
		}
		if got.ID() != cfg.SiteID {
			t.Errorf("ForURL(%s) = %s, want %s", cfg.Domain, got.ID(), cfg.SiteID)
		}
	}
}

func TestYesGirlzListsScenesNotVideos(t *testing.T) {
	var cfg paysiteutil.SiteConfig
	for _, c := range sites {
		if c.SiteID == "yesgirlz" {
			cfg = c
		}
	}
	if cfg.ListPath != "scenes" {
		t.Fatalf("ListPath = %q, want scenes — /videos 404s on that site", cfg.ListPath)
	}
	if got := paysiteutil.New(cfg).Patterns()[1]; got != "yesgirlz.com/scenes" {
		t.Errorf("Patterns()[1] = %q", got)
	}
}
