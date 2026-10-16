//go:build integration

package shopmaker

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

func TestLiveAndyStarModes(t *testing.T) {
	cfg := sites[len(sites)-1]
	for name, u := range map[string]string{
		"category": "https://www.andy-star-videos.com/collections?category=Milf",
		"model":    "https://www.andy-star-videos.com/models/dirty-priscilla",
	} {
		t.Run(name, func(t *testing.T) {
			testutil.RunLiveScrape(t, New(cfg), u, 3)
		})
	}
}
