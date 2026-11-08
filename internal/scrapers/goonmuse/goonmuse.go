// Package goonmuse registers goonmuse.com, an Elevated X `latestUpdateB` tour (see
// latestupdateutil).
package goonmuse

import (
	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = latestupdateutil.SiteConfig{
	SiteID:     "goonmuse",
	SiteBase:   "https://goonmuse.com",
	StudioName: "GoonMuse",
	Patterns: []string{
		"goonmuse.com",
		"goonmuse.com/categories/movies.html",
		"goonmuse.com/models/{slug}.html",
		"goonmuse.com/dvds/{slug}.html",
	},
}

func init() { scraper.Register(latestupdateutil.New(site)) }
