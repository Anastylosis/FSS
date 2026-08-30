// Package paysitemanager registers the studios running the PaySiteManager
// tour. They share no network — each bought the same tour software — so they
// are grouped by template. The template itself lives in paysitemanagerutil.
package paysitemanager

import (
	"github.com/Anastylosis/FSS/internal/scrapers/paysitemanagerutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []paysitemanagerutil.SiteConfig{
	{SiteID: "thesensitivespot", SiteBase: "https://thesensitivespot.com", StudioName: "The Sensitive Spot"},
	{SiteID: "fetishpros", SiteBase: "https://fetishpros.com", StudioName: "Fetish Pros"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(paysitemanagerutil.New(cfg))
	}
}
