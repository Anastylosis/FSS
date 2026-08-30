// Package brandnewamateurs registers the Brand New Amateurs network. All three
// sites run the classic Elevated X tour (`item-thumb` cards,
// `/categories/movies/{N}/latest/` pagination, `/trailers/{slug}.html` detail
// pages) at the site root, which adultdoorwayclassicutil already parses, so
// this is three config rows.
package brandnewamateurs

import (
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/adultdoorwayclassicutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []adultdoorwayclassicutil.SiteConfig{
	{
		ID:       "brandnewamateurs",
		SiteBase: "https://brandnewamateurs.com",
		Studio:   "Brand New Amateurs",
		Patterns: []string{
			"brandnewamateurs.com",
			"brandnewamateurs.com/categories/movies/{page}/latest/",
			"brandnewamateurs.com/categories/{slug}/{page}/latest/",
		},
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?brandnewamateurs\.com(?:/|$)`),
	},
	{
		ID:       "footjobvirgin",
		SiteBase: "https://footjobvirgin.com",
		Studio:   "Foot Job Virgin",
		Patterns: []string{
			"footjobvirgin.com",
			"footjobvirgin.com/categories/movies/{page}/latest/",
			"footjobvirgin.com/categories/{slug}/{page}/latest/",
		},
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?footjobvirgin\.com(?:/|$)`),
	},
	{
		ID:       "jackoffgirls",
		SiteBase: "https://jackoffgirls.com",
		Studio:   "Jack Off Girls",
		Patterns: []string{
			"jackoffgirls.com",
			"jackoffgirls.com/categories/movies/{page}/latest/",
			"jackoffgirls.com/categories/{slug}/{page}/latest/",
		},
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?jackoffgirls\.com(?:/|$)`),
	},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(adultdoorwayclassicutil.New(cfg))
	}
}
