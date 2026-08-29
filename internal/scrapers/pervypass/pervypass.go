// Package pervypass registers the PervyPass network — pawged.com, onlybbc.com
// and pawgnextdoor.com — which run the Elevated X "updateItem" tour under a
// /tour prefix. The template itself lives in elxupdateutil.
package pervypass

import (
	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []elxupdateutil.SiteConfig{
	// justpov.com redirects to pawged.com and serves its tour, so it is an
	// alias rather than a second catalogue.
	{SiteID: "pawged", Domain: "pawged.com", StudioName: "PAWGED", TourPrefix: "/tour", Aliases: []string{"justpov.com"}},
	{SiteID: "onlybbc", Domain: "onlybbc.com", StudioName: "Only BBC", TourPrefix: "/tour"},
	{SiteID: "pawgnextdoor", Domain: "pawgnextdoor.com", StudioName: "PAWG NEXT DOOR", TourPrefix: "/tour"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(elxupdateutil.New(cfg))
	}
}

// newFor builds the scraper for a site id. Used by tests.
func newFor(siteID string) *elxupdateutil.Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return elxupdateutil.New(cfg)
		}
	}
	return nil
}
