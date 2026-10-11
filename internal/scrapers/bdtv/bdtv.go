// Package bdtv registers the studios of the bdtv.com / harmonyvideo.tv VOD
// storefront. See docs/scrapers.md.
package bdtv

import (
	"github.com/Anastylosis/FSS/internal/scrapers/bdtvutil"
	"github.com/Anastylosis/FSS/scraper"
)

const base = "https://bdtv.com"

var sites = []bdtvutil.SiteConfig{
	{SiteID: "harmonyconcepts", StudioName: "Harmony Concepts", StudioID: 66, Domains: []string{"harmonyvideo.tv"}, Base: base},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(bdtvutil.New(cfg))
	}
}
