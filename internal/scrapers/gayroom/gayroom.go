// Package gayroom registers the Gay Room network. Every site in it runs the
// same FYC/PornPros Nuxt tour as the straight PornPros brands, so the whole
// network is 14 config rows over fycutil.
package gayroom

import (
	"github.com/Anastylosis/FSS/internal/scrapers/fycutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []fycutil.SiteConfig{
	{SiteID: "gayroom", Domain: "gayroom.com", StudioName: "Gay Room"},
	{SiteID: "bathhousebait", Domain: "bathhousebait.com", StudioName: "Bath House Bait"},
	{SiteID: "boysdestroyed", Domain: "boysdestroyed.com", StudioName: "Boys Destroyed"},
	{SiteID: "damnthatsbig", Domain: "damnthatsbig.com", StudioName: "Damn That's Big"},
	{SiteID: "gaycastings", Domain: "gaycastings.com", StudioName: "Gay Castings"},
	{SiteID: "gaycreeps", Domain: "gaycreeps.com", StudioName: "Gay Creeps"},
	{SiteID: "gayviolations", Domain: "gayviolations.com", StudioName: "Gay Violations"},
	{SiteID: "manroyale", Domain: "manroyale.com", StudioName: "Man Royale"},
	{SiteID: "massagebait", Domain: "massagebait.com", StudioName: "Massage Bait"},
	{SiteID: "menpov", Domain: "menpov.com", StudioName: "Men POV"},
	{SiteID: "officecock", Domain: "officecock.com", StudioName: "Office Cock"},
	{SiteID: "outhim", Domain: "outhim.com", StudioName: "Out Him"},
	{SiteID: "showerbait", Domain: "showerbait.com", StudioName: "Shower Bait"},
	{SiteID: "thickandbig", Domain: "thickandbig.com", StudioName: "Thick and Big"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(fycutil.New(cfg))
	}
}
