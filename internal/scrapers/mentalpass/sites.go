package mentalpass

import "github.com/Anastylosis/FSS/scraper"

var sites = []SiteConfig{
	{SiteID: "bitchstop", Domain: "bitchstop.com", StudioName: "Bitch Stop"},
	{SiteID: "czasting", Domain: "czasting.com", StudioName: "CZasting"},
	{SiteID: "czechgfs", Domain: "czechgfs.com", StudioName: "Czech GFS"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}
