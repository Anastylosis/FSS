// Package darkkotv registers darkkotv.com, an Elevated X `latestUpdateB` tour (see
// latestupdateutil).
package darkkotv

import (
	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = latestupdateutil.SiteConfig{
	SiteID:     "darkkotv",
	SiteBase:   "https://darkkotv.com",
	StudioName: "Darkko TV",
	Patterns: []string{
		"darkkotv.com",
		"darkkotv.com/categories/movies.html",
		"darkkotv.com/models/{slug}.html",
		"darkkotv.com/dvds/{slug}.html",
	},
}

func init() { scraper.Register(latestupdateutil.New(site)) }
