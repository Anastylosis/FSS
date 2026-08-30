// Package blusherotica registers blusheroticavr.com. It runs the same Elevated
// X `latestUpdateB` tour as Sofie Marie (see latestupdateutil).
package blusherotica

import (
	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = latestupdateutil.SiteConfig{
	SiteID:     "blusheroticavr",
	SiteBase:   "https://blusheroticavr.com",
	StudioName: "Blush Erotica VR",
	Patterns: []string{
		"blusheroticavr.com",
		"blusheroticavr.com/models/{slug}.html",
		"blusheroticavr.com/dvds/{slug}.html",
	},
}

func init() { scraper.Register(latestupdateutil.New(site)) }
