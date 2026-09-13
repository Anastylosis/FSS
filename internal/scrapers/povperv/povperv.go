// Package povperv registers POV Perv and its sister site NylonPERV, both on
// the Paysite.com Next.js template (see paysiteutil).
//
// POV Perv serves its listing at /scenes — /videos is a 308 to it — and lives
// on the tour subdomain, with the apex redirecting there; the apex is carried
// as an alias so either spelling scrapes.
package povperv

import (
	"github.com/Anastylosis/FSS/internal/scrapers/paysiteutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []paysiteutil.SiteConfig{
	{
		SiteID:     "povperv",
		Domain:     "tour.povperv.com",
		StudioName: "POV Perv",
		ListPath:   "scenes",
		Aliases:    []string{"povperv.com"},
	},
	{SiteID: "nylonperv", Domain: "nylonperv.com", StudioName: "NylonPERV"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(paysiteutil.New(cfg))
	}
}
