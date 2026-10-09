// Package xbrats registers xbrats.com (Bratty Foot Girls), an Elevated X
// `latestUpdateB` tour (see latestupdateutil).
package xbrats

import (
	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = latestupdateutil.SiteConfig{
	SiteID:     "xbrats",
	SiteBase:   "https://xbrats.com",
	StudioName: "Bratty Foot Girls",
	Patterns: []string{
		"xbrats.com",
		"xbrats.com/categories/movies.html",
		"xbrats.com/models/{slug}.html",
		"xbrats.com/dvds/{slug}.html",
	},
}

func init() { scraper.Register(latestupdateutil.New(site)) }
