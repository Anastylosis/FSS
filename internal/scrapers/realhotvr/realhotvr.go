// Package realhotvr registers realhotvr.com. It runs the classic Elevated X
// tour that adultdoorwayclassicutil parses, at the site root.
package realhotvr

import (
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/adultdoorwayclassicutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = adultdoorwayclassicutil.SiteConfig{
	ID:       "realhotvr",
	SiteBase: "https://www.realhotvr.com",
	Studio:   "RealHotVR",
	Patterns: []string{
		"realhotvr.com",
		"realhotvr.com/categories/movies/{page}/latest/",
		"realhotvr.com/categories/{slug}/{page}/latest/",
	},
	MatchRe: regexp.MustCompile(`^https?://(?:www\.)?realhotvr\.com(?:/|$)`),
}

func init() { scraper.Register(adultdoorwayclassicutil.New(site)) }
