//go:build integration

package grooby

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/groobyutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// siteByID looks the config up by name rather than by table index, which moves
// whenever a site is added.
func siteByID(t *testing.T, id string) groobyutil.SiteConfig {
	t.Helper()
	for _, c := range sites {
		if c.SiteID == id {
			return c
		}
	}
	t.Fatalf("site %q not registered", id)
	return groobyutil.SiteConfig{}
}

// The older card shape: credit, runtime and date inside the `sexyvideo` div.
func TestLiveGroobyGirls(t *testing.T) {
	testutil.RunLiveScrape(t, groobyutil.New(siteByID(t, "groobygirls")), "https://www.groobygirls.com/tour/", 2)
}

// The newer `sexyvideo_outer` shape, where those three fields sit outside it.
func TestLiveUKTGirls(t *testing.T) {
	testutil.RunLiveScrape(t, groobyutil.New(siteByID(t, "uktgirls")), "https://uk-tgirls.com/tour/", 2)
}

// TransErotica serves its tour from a subdomain and has no /tour prefix, so it
// also exercises the apex-only `www.` rule.
func TestLiveTransErotica(t *testing.T) {
	testutil.RunLiveScrape(t, groobyutil.New(siteByID(t, "transerotica")), "https://tour.transerotica.com/", 2)
}

// A TransErotica performer sub-tour: same CMS at the document root, and every
// card links through the NATS affiliate redirect rather than the scene's own
// URL.
func TestLiveCherryMavrik(t *testing.T) {
	testutil.RunLiveScrape(t, groobyutil.New(siteByID(t, "cherrymavrik")), "https://cherrymavrik.com/", 2)
}

func TestLiveNaughtyNatural(t *testing.T) {
	testutil.RunLiveScrape(t, groobyutil.New(siteByID(t, "naughtynatural")), "https://www.naughtynatural.com/tour/", 3)
}
