// Package helenascockquest registers experiencehelenaprice.com, the site
// StashDB tracks as "Helenas Cock Quest". It runs the same Elevated X
// `latestUpdateB` tour as Sofie Marie (see latestupdateutil).
package helenascockquest

import (
	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = latestupdateutil.SiteConfig{
	SiteID:     "helenascockquest",
	SiteBase:   "https://experiencehelenaprice.com",
	StudioName: "Helenas Cock Quest",
	Patterns: []string{
		"experiencehelenaprice.com",
		"experiencehelenaprice.com/models/{slug}.html",
		"experiencehelenaprice.com/dvds/{slug}.html",
	},
}

func init() { scraper.Register(latestupdateutil.New(site)) }
