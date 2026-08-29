// Package brokenlatinawhores registers brokenlatinawhores.com. It runs the
// same Elevated X "updateItem" tour as the PervyPass network (see
// elxupdateutil), mounted at the site root and with its catalogue filed under
// the "updates" slug rather than "movies".
package brokenlatinawhores

import (
	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = elxupdateutil.SiteConfig{
	SiteID:     "brokenlatinawhores",
	Domain:     "brokenlatinawhores.com",
	StudioName: "Broken Latina Whores",
	ListSlug:   "updates",
	BareHost:   true,
}

func init() { scraper.Register(elxupdateutil.New(site)) }
