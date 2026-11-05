// Package joeysfeetgirls registers joeysfeetgirls.com, an Elevated X
// `latestUpdateB` tour (see latestupdateutil).
package joeysfeetgirls

import (
	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = latestupdateutil.SiteConfig{
	SiteID:     "joeysfeetgirls",
	SiteBase:   "https://www.joeysfeetgirls.com",
	StudioName: "Joey's FeetGirls",
	Patterns: []string{
		"joeysfeetgirls.com",
		"joeysfeetgirls.com/models/{slug}.html",
		"joeysfeetgirls.com/dvds/{slug}.html",
	},
}

func init() { scraper.Register(latestupdateutil.New(site)) }
