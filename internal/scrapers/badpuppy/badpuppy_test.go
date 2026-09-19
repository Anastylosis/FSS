package badpuppy

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/natscmsutil"
)

// The area id is scoped to PuppyCash's backend; pointed at Mars Media's it
// answers "Invalid Area" with HTTP 403.
func TestSiteUsesItsOwnNatsBackend(t *testing.T) {
	if site.NatsAPIBase != "https://nats.puppycash.com/tour_api.php" {
		t.Errorf("NatsAPIBase = %q", site.NatsAPIBase)
	}
	if site.CMSAreaID == "" || site.StudioName != "BadPuppy" {
		t.Errorf("CMSAreaID/StudioName = %q/%q", site.CMSAreaID, site.StudioName)
	}
	s := natscmsutil.New(site)
	for _, u := range []string{"https://badpuppy.com/", "https://www.badpuppy.com", "https://tour.badpuppy.com/"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	if s.MatchesURL("https://badpuppy.net/") {
		t.Error("MatchesURL matched another domain")
	}
}
