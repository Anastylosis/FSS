// Package joshstone registers joshstonexxx.com, an Elevated X "updateItem"
// tour (see elxupdateutil) mounted at /tour with the default "movies"
// catalogue slug.
package joshstone

import (
	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = elxupdateutil.SiteConfig{
	SiteID:     "joshstone",
	Domain:     "joshstonexxx.com",
	StudioName: "Josh Stone",
	TourPrefix: "/tour",
}

func init() { scraper.Register(elxupdateutil.New(site)) }
