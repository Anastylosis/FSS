// Package blackbullchallenge registers blackbullchallenge.com, an Elevated X `latestUpdateB` tour (see
// latestupdateutil).
package blackbullchallenge

import (
	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = latestupdateutil.SiteConfig{
	SiteID:     "blackbullchallenge",
	SiteBase:   "https://blackbullchallenge.com",
	StudioName: "Black Bull Challenge",
	Patterns: []string{
		"blackbullchallenge.com",
		"blackbullchallenge.com/categories/movies.html",
		"blackbullchallenge.com/models/{slug}.html",
		"blackbullchallenge.com/dvds/{slug}.html",
	},
}

func init() { scraper.Register(latestupdateutil.New(site)) }
