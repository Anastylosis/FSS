// Package mysweetapple registers mysweetapple.com. Its tour moved to Elevated
// X's `latestUpdateB` theme in 2026-10; the template lives in
// latestupdateutil and this package is the site row.
package mysweetapple

import (
	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = latestupdateutil.SiteConfig{
	SiteID:     "mysweetapple",
	SiteBase:   "https://mysweetapple.com",
	StudioName: "MySweetApple",
	Patterns: []string{
		"mysweetapple.com",
		"mysweetapple.com/categories/movies.html",
		"mysweetapple.com/models/{slug}.html",
		"mysweetapple.com/dvds/{slug}.html",
	},
}

func init() { scraper.Register(latestupdateutil.New(site)) }
