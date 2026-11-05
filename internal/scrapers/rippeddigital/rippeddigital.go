// Package rippeddigital registers ripped.digital, an Elevated X `latestUpdateB`
// tour (see latestupdateutil).
package rippeddigital

import (
	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = latestupdateutil.SiteConfig{
	SiteID:     "rippeddigital",
	SiteBase:   "https://ripped.digital",
	StudioName: "Ripped Digital",
	Patterns: []string{
		"ripped.digital",
		"ripped.digital/models/{slug}.html",
		"ripped.digital/dvds/{slug}.html",
	},
}

func init() { scraper.Register(latestupdateutil.New(site)) }
