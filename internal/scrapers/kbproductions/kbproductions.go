// Package kbproductions registers KB Productions network sites running the
// Paysite.com Next.js template. The template itself lives in paysiteutil;
// sites using the older IndieBucks template are in the indiebucks package.
package kbproductions

import (
	"github.com/Anastylosis/FSS/internal/scrapers/paysiteutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []paysiteutil.SiteConfig{
	{SiteID: "melinamay", Domain: "melina-may.com", StudioName: "Melina-May"},
	{SiteID: "passionpov", Domain: "passionpov.com", StudioName: "PassionPOV"},
	{SiteID: "shehergirls", Domain: "shehergirls.com", StudioName: "She Her Girls"},
	{SiteID: "vrallure", Domain: "vrallure.com", StudioName: "VR Allure", ModelPattern: "vrallure.com/models/{id}-{slug}"},
	{SiteID: "manpuppy", Domain: "manpuppy.com", StudioName: "ManPuppy", ModelPattern: "manpuppy.com/models/{id}-{slug}"},
	{SiteID: "milflicious", Domain: "milflicious.com", StudioName: "Milflicious", ModelPattern: "milflicious.com/models/{id}-{slug}"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(paysiteutil.New(cfg))
	}
}
