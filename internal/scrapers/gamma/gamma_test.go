package gamma

import (
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteCount(t *testing.T) {
	if len(sites) != 180 {
		t.Errorf("expected 180 sites, got %d", len(sites))
	}
}

func TestScraperInterface(t *testing.T) {
	for _, cfg := range sites {
		_ = cfg
		var _ scraper.StudioScraper = &siteScraper{}
	}
}

func TestUniqueSiteIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, cfg := range sites {
		if seen[cfg.SiteID] {
			t.Errorf("duplicate SiteID: %s", cfg.SiteID)
		}
		seen[cfg.SiteID] = true
	}
}

// TestPrideStudiosSites pins the Pride Studios family added to the ASGMAX
// segment. The brands split into own-domain sites and sites that redirect to
// pridestudios.com; the latter need RefererBase pinned to the hub or the
// Referer-restricted Algolia key is rejected with HTTP 403.
func TestPrideStudiosSites(t *testing.T) {
	byID := map[string]siteConfig{}
	for _, cfg := range sites {
		byID[cfg.SiteID] = cfg
	}

	ownDomain := []string{"extrabigdicks", "menover30", "familycreep", "pridestudios"}
	redirecting := []string{
		"circlejerkboys", "boyzparty", "highperformancemen",
		"dylanlucas", "cockvirgins", "bearback",
	}

	for _, id := range append(append([]string{}, ownDomain...), redirecting...) {
		cfg, ok := byID[id]
		if !ok {
			t.Errorf("missing Pride Studios site %q", id)
			continue
		}
		// SiteName is the Algolia availableOnSite filter. Leaving it empty
		// would drop the filter and return the whole 13k-scene asgmax
		// segment instead of this brand.
		if cfg.SiteName != id {
			t.Errorf("%s: SiteName = %q, want %q", id, cfg.SiteName, id)
		}
		if cfg.StudioName == "" {
			t.Errorf("%s: StudioName is empty", id)
		}
	}

	for _, id := range ownDomain {
		if got := byID[id].RefererBase; got != "" {
			t.Errorf("%s serves its own /en/videos, so RefererBase should be empty, got %q", id, got)
		}
	}
	for _, id := range redirecting {
		if got := byID[id].RefererBase; got != "https://www.pridestudios.com" {
			t.Errorf("%s redirects to the hub, so RefererBase must be pinned there, got %q", id, got)
		}
	}
}

// The fallthrough warning reads Patterns(), so the forms gammautil actually
// dispatches on must be listed or every performer and series URL on the
// platform warns about a fallthrough that is not happening — and the forms it
// does *not* dispatch on must stay absent, since scraping a channel URL really
// does return the segment's whole catalogue under that channel's key.
func TestURLRecognitionMatchesWhatRunDispatchesOn(t *testing.T) {
	s, err := scraper.ForURL("https://www.adulttime.com/")
	if err != nil {
		t.Fatal(err)
	}

	for _, u := range []string{
		"https://www.adulttime.com/",
		"https://www.adulttime.com/en/videos",
		"https://www.adulttime.com/en/pornstar/view/someone/12345",
		"https://www.adulttime.com/en/model/view/someone/12345",
		"https://www.adulttime.com/en/serie/9876/",
	} {
		if scraper.URLLooksUnhandled(s, u) {
			t.Errorf("%s is dispatched on but reads as a fallthrough", u)
		}
	}

	// No facet is derived from a channel or category, so these fall through to
	// the segment's whole catalogue and must say so.
	for _, u := range []string{
		"https://www.adulttime.com/en/channel/some-channel",
		"https://www.adulttime.com/en/category/anal",
	} {
		if !scraper.URLLooksUnhandled(s, u) {
			t.Errorf("%s falls through to the full segment and must warn", u)
		}
	}
}

// A row that carries its own match regex exists because that channel or studio
// path is the filtered view it scrapes, and it narrows the index by
// availableOnSite. Those URLs must not warn.
func TestPinnedRowsVouchForTheirOwnURL(t *testing.T) {
	for _, u := range []string{
		"https://www.adulttime.com/en/studio/adult-time",
		"https://www.adulttime.com/en/channel/adult-time-originals",
		"https://www.nextdoorstudios.com/en/videos/sites/strokethatdick",
	} {
		s, err := scraper.ForURL(u)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		if scraper.URLLooksUnhandled(s, u) {
			t.Errorf("%s routed to %s, which is scoped to it, but reads as a fallthrough", u, s.ID())
		}
	}
}
