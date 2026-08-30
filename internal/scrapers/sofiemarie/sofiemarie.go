// Package sofiemarie registers sofiemariexxx.com. The template — Elevated X's
// `latestUpdateB` theme — lives in latestupdateutil; this package is the site
// row.
//
// yummysofie.com is an alias, not a second catalogue: it serves the same pages
// with every self-link rewritten to its own host, and both join through
// join.sofiemariexxx.com. It stays one SiteID fetching one SiteBase, because
// two would ingest the whole catalogue twice.
package sofiemarie

import (
	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = latestupdateutil.SiteConfig{
	SiteID:     "sofiemarie",
	SiteBase:   "https://sofiemariexxx.com",
	StudioName: "Sofie Marie",
	AltDomains: []string{"yummysofie.com"},
	Patterns: []string{
		"sofiemariexxx.com",
		"sofiemariexxx.com/models/{slug}.html",
		"sofiemariexxx.com/dvds/{slug}.html",
		"yummysofie.com (rewritten to sofiemariexxx.com)",
	},
}

func init() { scraper.Register(latestupdateutil.New(site)) }
