// Package kbproductions registers KB Productions network sites running the
// Paysite.com Next.js template. The template itself lives in paysiteutil;
// sites using the older IndieBucks template are in the indiebucks package, and
// VRAllure and ManPuppy, which moved to other YPP tour themes, are in ypptour.
package kbproductions

import (
	"github.com/Anastylosis/FSS/internal/scrapers/paysiteutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []paysiteutil.SiteConfig{
	{SiteID: "melinamay", Domain: "melina-may.com", StudioName: "Melina-May"},
	{SiteID: "passionpov", Domain: "passionpov.com", StudioName: "PassionPOV"},
	{SiteID: "shehergirls", Domain: "shehergirls.com", StudioName: "She Her Girls"},
	{SiteID: "milflicious", Domain: "milflicious.com", StudioName: "Milflicious", ModelPattern: "milflicious.com/models/{id}-{slug}"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(paysiteutil.New(cfg))
	}
}
