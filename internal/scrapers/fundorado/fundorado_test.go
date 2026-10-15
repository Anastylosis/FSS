package fundorado

import (
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestRowsRouteToTheirOwnScraper(t *testing.T) {
	want := map[string]string{
		"https://orgasmabuse.com/":                                    "orgasmabuse",
		"https://www.rfmovies.com":                                    "rfmovies",
		"https://rubber-empire.com/videos/browse/labels/751":          "rubberempire",
		"https://taworship.com/videos/browse/genres/236?ts=abc":       "taworship",
		"https://tickleabuse.com/videos/browse/studio/2354":           "tickleabuse",
		"https://orgasmabuse.com/videos/browse/genre/12":              "orgasmabuse",
		"http://www.tickleabuse.com/":                                 "tickleabuse",
		"https://rfmovies.com/videos/browse/labels/695":               "rfmovies",
		"https://www.rubber-empire.com/videos/browse/studio/3482?x=1": "rubberempire",
	}
	for url, id := range want {
		s, err := scraper.ForURL(url)
		if err != nil {
			t.Errorf("%s: %v", url, err)
			continue
		}
		if s.ID() != id {
			t.Errorf("%s routed to %s, want %s", url, s.ID(), id)
		}
	}
}

func TestSiteRowsAreDistinct(t *testing.T) {
	ids, hosts := map[string]bool{}, map[string]bool{}
	for _, cfg := range sites {
		if ids[cfg.SiteID] || hosts[cfg.Host] {
			t.Errorf("duplicate row %+v", cfg)
		}
		ids[cfg.SiteID], hosts[cfg.Host] = true, true
		if len(cfg.Labels) == 0 || cfg.StudioName == "" {
			t.Errorf("incomplete row %+v", cfg)
		}
	}
	// The United Content brands share one label catalogue; a label in two
	// rows would store its scenes twice under different site ids.
	seen := map[int]string{}
	for _, cfg := range sites {
		for _, l := range cfg.Labels {
			if other, ok := seen[l]; ok {
				t.Errorf("label %d is in both %s and %s", l, other, cfg.SiteID)
			}
			seen[l] = cfg.SiteID
		}
	}
}
