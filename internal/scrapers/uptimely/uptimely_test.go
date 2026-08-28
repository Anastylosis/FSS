package uptimely

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/uptimelyutil"
)

func TestSiteCount(t *testing.T) {
	if len(sites) != 16 {
		t.Errorf("expected 16 sites, got %d", len(sites))
	}
}

// buildScraper reuses init()'s own matcher rather than restating the pattern,
// so the two cannot drift apart.
func buildScraper(cfg siteConfig) *uptimelyutil.Scraper {
	return uptimelyutil.New(uptimelyutil.SiteConfig{
		ID:      cfg.SiteID,
		Studio:  cfg.StudioName,
		Domain:  cfg.Domain,
		MatchRe: matchRe(cfg.Domain),
	})
}

func TestNewSitesMatchURLs(t *testing.T) {
	cases := map[string][]string{
		"oppai": {
			"https://oppai-av.com/works/list/release",
			"https://oppai-av.com/actress/detail/123",
		},
		"ebody": {
			"https://av-e-body.com/works/list/release",
			"https://www.av-e-body.com/actress/detail/45",
		},
		// The bare host is the whole-catalogue entry point and must match, or
		// `fss scrape https://tameikegoro.jp/` reports the site unsupported.
		"tameikegoro": {
			"https://tameikegoro.jp",
			"https://tameikegoro.jp/",
			"https://www.tameikegoro.jp/works/list/genre/113",
		},
	}
	for id, urls := range cases {
		s := buildScraper(findSiteCfg(id))
		for _, u := range urls {
			if !s.MatchesURL(u) {
				t.Errorf("%s: expected MatchesURL(%q) = true", id, u)
			}
		}
		if s.MatchesURL("https://example.com/works/list/release") {
			t.Errorf("%s: should not match unrelated domain", id)
		}
	}
}

func findSiteCfg(id string) siteConfig {
	for _, c := range sites {
		if c.SiteID == id {
			return c
		}
	}
	panic("site not found: " + id)
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
