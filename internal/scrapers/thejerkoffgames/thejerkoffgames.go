// Package thejerkoffgames registers thejerkoffmembers.com, an Elevated X `latestUpdateB` tour (see
// latestupdateutil).
package thejerkoffgames

import (
	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = latestupdateutil.SiteConfig{
	SiteID:     "thejerkoffgames",
	SiteBase:   "https://www.thejerkoffmembers.com",
	StudioName: "The Jerk Off Games",
	AltDomains: []string{"thejerkoffgames.com", "thejoigames.com"},
	Patterns: []string{
		"thejerkoffmembers.com",
		"thejerkoffmembers.com/categories/movies.html",
		"thejerkoffmembers.com/models/{slug}.html",
		"thejerkoffmembers.com/dvds/{slug}.html",
		"thejerkoffgames.com",
		"thejoigames.com",
	},
}

func init() { scraper.Register(latestupdateutil.New(site)) }
