// Package only3xnetwork registers the Only 3x tours that run the
// mpp-playlist Elevated X template (elxplaylistutil). See docs/scrapers.md.
package only3xnetwork

import (
	"github.com/Anastylosis/FSS/internal/scrapers/elxplaylistutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []elxplaylistutil.SiteConfig{
	{SiteID: "only3xvr", Domain: "only3xvr.com", StudioName: "Only 3x VR", ListingOnly: true},
	{SiteID: "only3xlost", Domain: "only3xlost.com", StudioName: "Only 3x Lost"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(elxplaylistutil.New(cfg))
	}
}
