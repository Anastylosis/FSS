// Package trueamateurmodels registers trueamateurmodels.com. Its preview tour
// is the Elevated X "updateItem" theme, but every card links to the join page,
// so scenes come from the listing alone (see elxupdateutil's ListingOnly).
package trueamateurmodels

import (
	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = elxupdateutil.SiteConfig{
	SiteID:      "trueamateurmodels",
	Domain:      "trueamateurmodels.com",
	StudioName:  "True Amateur Models",
	TourPrefix:  "/previewtour",
	BareHost:    true,
	ListingOnly: true,
}

func init() { scraper.Register(elxupdateutil.New(site)) }
