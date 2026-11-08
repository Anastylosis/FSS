// Package tnvgirls registers tnvgirls.com, an Elevated X `updateItem` tour
// mounted under /tour (see elxupdateutil).
package tnvgirls

import (
	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = elxupdateutil.SiteConfig{
	SiteID:      "tnvgirls",
	Domain:      "tnvgirls.com",
	StudioName:  "TNV Girls",
	TourPrefix:  "/tour",
	ListingOnly: true,
}

func init() { scraper.Register(elxupdateutil.New(site)) }
