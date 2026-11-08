// Package tiedgirls registers tiedgirls.com, an Elevated X `latestUpdateB` tour (see
// latestupdateutil).
package tiedgirls

import (
	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = latestupdateutil.SiteConfig{
	SiteID:     "tiedgirls",
	SiteBase:   "https://www.tiedgirls.com",
	StudioName: "Tied Girls",
	Patterns: []string{
		"tiedgirls.com",
		"tiedgirls.com/categories/movies.html",
		"tiedgirls.com/models/{slug}.html",
		"tiedgirls.com/dvds/{slug}.html",
	},
}

func init() { scraper.Register(latestupdateutil.New(site)) }
