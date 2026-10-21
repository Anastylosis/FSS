// Package bdtv registers the studios of the bdtv.com / harmonyvideo.tv VOD
// storefront. See docs/scrapers.md.
package bdtv

import (
	"github.com/Anastylosis/FSS/internal/scrapers/bdtvutil"
	"github.com/Anastylosis/FSS/scraper"
)

const base = "https://bdtv.com"

var sites = []bdtvutil.SiteConfig{
	{SiteID: "closeup", StudioName: "Close-Up Entertainment", StudioID: 58, Domains: []string{"bdtv.com"}, Base: base},
	{SiteID: "harmonyconcepts", StudioName: "Harmony Concepts", StudioID: 66, Domains: []string{"harmonyvideo.tv"}, Base: base},
	// Studios with no domain of their own are reached only by their
	// /studio-display?studio_id= URL.
	{SiteID: "curtisdupontwrestling", StudioName: "Curtis Dupont Wrestling", StudioID: 75, Base: base},
	{SiteID: "helplessheroes", StudioName: "Helpless Heroes", StudioID: 71, Base: base},
	{SiteID: "matrockwrestling", StudioName: "Matrock Wrestling", StudioID: 69, Base: base},
	{SiteID: "taoproductions", StudioName: "Tao Productions", StudioID: 59, Base: base},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(bdtvutil.New(cfg))
	}
}
