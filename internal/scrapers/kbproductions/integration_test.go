//go:build integration

package kbproductions

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/paysiteutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// live looks a site up by ID rather than slice index — removing a config row
// must not silently repoint these tests at a different site.
func live(t *testing.T, id, studioURL string) {
	t.Helper()
	for _, c := range sites {
		if c.SiteID == id {
			testutil.RunLiveScrape(t, paysiteutil.New(c), studioURL, 3)
			return
		}
	}
	t.Fatalf("site not found: %s", id)
}

func TestLiveMelinaMay(t *testing.T)   { live(t, "melinamay", "https://melina-may.com/videos") }
func TestLivePassionPOV(t *testing.T)  { live(t, "passionpov", "https://passionpov.com/videos") }
func TestLiveMilflicious(t *testing.T) { live(t, "milflicious", "https://milflicious.com/videos") }
