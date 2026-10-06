//go:build integration

package kvssitemap

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestLiveSites(t *testing.T) {
	cases := []struct{ id, url string }{
		{"barbisinclair", "https://barbisinclair.com"},
		{"barbisinclair", "https://barbisinclair.com/models/alexa-jaymes/"},
		{"barbisinclair", "https://barbisinclair.com/tags/amateur/"},
		{"lafranceapoil", "https://www.lafranceapoil.com/"},
		{"lafranceapoil", "https://www.lafranceapoil.com/models/phoebe/"},
		{"lafranceapoil", "https://www.lafranceapoil.com/categories/amatrice/"},
		{"lafranceapoil", "https://www.lafranceapoil.com/series/les-lecons-hard-de-lhermite/"},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			s, err := scraper.ForURL(c.url)
			if err != nil || s.ID() != c.id {
				t.Fatalf("ForURL(%s) = %v, %v", c.url, s, err)
			}
			testutil.RunLiveScrape(t, s, c.url, 3)
		})
	}
}
