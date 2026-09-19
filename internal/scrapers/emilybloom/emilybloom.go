// Package emilybloom registers emilybloom.com. It runs the Elevated X
// "updateItem" tour (see elxupdateutil) at the site root, paginated as
// /updates/page_{N}.html rather than by category.
package emilybloom

import (
	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = elxupdateutil.SiteConfig{
	SiteID:        "emilybloom",
	Domain:        "emilybloom.com",
	StudioName:    "Emily Bloom",
	BareHost:      true,
	UpdatesPaging: true,
}

func init() { scraper.Register(elxupdateutil.New(site)) }
