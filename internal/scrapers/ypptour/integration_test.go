//go:build integration

package ypptour

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// live looks a site up by ID rather than slice index — inserting a config row
// must not silently repoint these tests at a different site.
func live(t *testing.T, id, studioURL string) {
	t.Helper()
	for _, c := range sites {
		if c.id == id {
			testutil.RunLiveScrape(t, New(c), studioURL, 3)
			return
		}
	}
	t.Fatalf("site not found: %s", id)
}

func TestLiveManPuppy(t *testing.T) { live(t, "manpuppy", "https://manpuppy.com/videos") }
func TestLiveVRAllure(t *testing.T) { live(t, "vrallure", "https://vrallure.com/") }

func TestLiveVRAllureModel(t *testing.T) {
	live(t, "vrallure", "https://vrallure.com/models/17822-breezy-bri")
}
