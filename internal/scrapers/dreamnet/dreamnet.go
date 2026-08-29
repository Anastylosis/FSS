// Package dreamnet registers the Dreamnet network sites that run the classic
// Elevated X tour (`item-thumb` cards, `/categories/movies/{N}/latest/`
// pagination, `/trailers/{slug}.html` detail pages) — the same template
// adultdoorwayclassicutil already parses. Each site mounts it under its own
// path prefix, which is the only per-site difference.
//
// Three network sites are not covered: jizzlocker.com answers 403 to every
// request, paytonhallxxx.com no longer resolves, and dreamnet.com itself is a
// WordPress support site with no catalogue.
package dreamnet

import (
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/adultdoorwayclassicutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []adultdoorwayclassicutil.SiteConfig{
	{
		ID:         "blowbanggirls",
		SiteBase:   "https://www.blowbanggirls.com",
		Studio:     "BlowBang Girls",
		TourPrefix: "/v3",
		Patterns: []string{
			"blowbanggirls.com",
			"blowbanggirls.com/v3/categories/movies/{page}/latest/",
			"blowbanggirls.com/v3/categories/{slug}/{page}/latest/",
		},
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?blowbanggirls\.com(?:/|$)`),
	},
	{
		ID:         "bathroomcreepers",
		SiteBase:   "https://www.bathroomcreepers.com",
		Studio:     "Bathroom Creepers",
		TourPrefix: "/creeper",
		Patterns: []string{
			"bathroomcreepers.com",
			"bathroomcreepers.com/creeper/categories/movies/{page}/latest/",
			"bathroomcreepers.com/creeper/categories/{slug}/{page}/latest/",
		},
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?bathroomcreepers\.com(?:/|$)`),
	},
	{
		ID:         "girlsdreamnet",
		SiteBase:   "https://girls.dreamnet.com",
		Studio:     "Girls.Dreamnet.Com",
		TourPrefix: "/tour",
		Patterns: []string{
			"girls.dreamnet.com",
			"girls.dreamnet.com/tour/categories/movies/{page}/latest/",
			"girls.dreamnet.com/tour/categories/{slug}/{page}/latest/",
		},
		MatchRe: regexp.MustCompile(`^https?://girls\.dreamnet\.com(?:/|$)`),
	},
	{
		ID:         "selfiesuck",
		SiteBase:   "https://www.selfiesuck.com",
		Studio:     "Selfie Suck",
		TourPrefix: "/tour",
		Patterns: []string{
			"selfiesuck.com",
			"selfiesuck.com/tour/categories/movies/{page}/latest/",
			"selfiesuck.com/tour/categories/{slug}/{page}/latest/",
		},
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?selfiesuck\.com(?:/|$)`),
	},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(adultdoorwayclassicutil.New(cfg))
	}
}
