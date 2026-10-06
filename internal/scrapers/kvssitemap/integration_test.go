//go:build integration

package kvssitemap

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestLiveBarbiSinclair(t *testing.T) {
	for _, u := range []string{
		"https://barbisinclair.com",
		"https://barbisinclair.com/models/alexa-jaymes/",
		"https://barbisinclair.com/tags/amateur/",
	} {
		t.Run(u, func(t *testing.T) {
			s, err := scraper.ForURL(u)
			if err != nil || s.ID() != "barbisinclair" {
				t.Fatalf("ForURL(%s) = %v, %v", u, s, err)
			}
			testutil.RunLiveScrape(t, s, u, 3)
		})
	}
}
