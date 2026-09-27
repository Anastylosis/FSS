//go:build integration

package arx

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func live(t *testing.T, id string) {
	t.Helper()
	for _, c := range sites {
		if c.SiteID == id {
			testutil.RunLiveScrape(t, newScraper(c), "https://"+c.Domain+"/scenes", 2)
			return
		}
	}
	t.Fatalf("site not found: %s", id)
}

func TestLiveHoneyTrans(t *testing.T)     { live(t, "honeytrans") }
func TestLiveJapanLust(t *testing.T)      { live(t, "japanlust") }
func TestLiveLesWorship(t *testing.T)     { live(t, "lesworship") }
func TestLiveJOIBabes(t *testing.T)       { live(t, "joibabes") }
func TestLivePOVMasters(t *testing.T)     { live(t, "povmasters") }
func TestLiveCuckHunter(t *testing.T)     { live(t, "cuckhunter") }
func TestLiveNudeYogaPorn(t *testing.T)   { live(t, "nudeyogaporn") }
func TestLiveTransRoommates(t *testing.T) { live(t, "transroommates") }

// Not in the CMS's embedded sites array, but on the same platform.
func TestLiveTransDaylight(t *testing.T) { live(t, "transdaylight") }
func TestLiveTransMidnight(t *testing.T) { live(t, "transmidnight") }
