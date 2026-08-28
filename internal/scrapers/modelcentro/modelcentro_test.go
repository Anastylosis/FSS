package modelcentro

import (
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteCount(t *testing.T) {
	if len(sites) != 39 {
		t.Errorf("expected 39 sites, got %d", len(sites))
	}
}

func TestScraperInterface(t *testing.T) {
	for _, cfg := range sites {
		s := &siteScraper{}
		var _ scraper.StudioScraper = s
		_ = cfg
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

func TestMatchesURL(t *testing.T) {
	cases := []struct {
		name   string
		siteID string
		url    string
		want   bool
	}{
		{"base URL", "thejerkygirls", "https://thejerkygirls.com", true},
		{"videos page", "thejerkygirls", "https://thejerkygirls.com/videos", true},
		{"videos trailing slash", "thejerkygirls", "https://thejerkygirls.com/videos/", true},
		{"www prefix", "mugurporn", "https://www.mugurporn.com/videos", true},
		{"with query", "thiccvision", "https://thiccvision.com/videos?page=2", true},
		{"scene page no match", "thejerkygirls", "https://thejerkygirls.com/scene/123/slug", false},
		{"wrong domain", "thejerkygirls", "https://example.com/videos", false},
		{".tv domain", "ricporter", "https://ricporter.tv/videos", true},
		{"www kinkyrubberworld", "kinkyrubberworld", "https://www.kinkyrubberworld.com/videos", true},
		{"kinkyrubberworld base", "kinkyrubberworld", "https://kinkyrubberworld.com", true},
		// naughtylada.com is a live redirect to the hyphenated domain, so both
		// must resolve to the same scraper.
		{"naughtylada canonical", "naughtylada", "https://naughty-lada.com/videos", true},
		{"naughtylada alias", "naughtylada", "https://naughtylada.com", true},
		{"naughtylada alias videos", "naughtylada", "https://www.naughtylada.com/videos/", true},
		{"alias is not a wildcard", "naughtylada", "https://naughty-lada.org", false},
	}

	lookup := map[string]*siteScraper{}
	for _, cfg := range sites {
		lookup[cfg.SiteID] = &siteScraper{matchRe: matchReFor(cfg)}
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, ok := lookup[c.siteID]
			if !ok {
				t.Fatalf("site %q not found", c.siteID)
			}
			if got := s.MatchesURL(c.url); got != c.want {
				t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
			}
		})
	}
}
