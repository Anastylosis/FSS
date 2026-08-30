package sofiemarie

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://sofiemariexxx.com/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "sofiemarie" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

// The alias must resolve to the one canonical catalogue: scraping
// yummysofie.com separately would ingest every scene a second time.
func TestAliasResolvesToTheSameScraper(t *testing.T) {
	got, err := scraper.ForURL("https://yummysofie.com/models/sofie-marie.html")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "sofiemarie" {
		t.Errorf("ForURL(alias) = %s, want sofiemarie", got.ID())
	}
	if site.SiteBase != "https://sofiemariexxx.com" {
		t.Errorf("SiteBase = %q — requests must go to the canonical host", site.SiteBase)
	}
}

func TestMatchesOwnDomainsOnly(t *testing.T) {
	s := latestupdateutil.New(site)
	for _, u := range []string{"https://sofiemariexxx.com/", "https://www.yummysofie.com/", "http://sofiemariexxx.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://sofiemariexxx.net/", "https://notsofiemariexxx.com/", "https://example.com/sofiemariexxx.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}
