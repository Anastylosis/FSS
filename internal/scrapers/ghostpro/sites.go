package ghostpro

import (
	"regexp"

	"github.com/Anastylosis/FSS/scraper"
)

// sites lists every Ghost Pro Productions sister site running the Next.js
// shop template. Each registers its own scraper (one ID per domain) so
// `fss list-scrapers` shows them individually and `fss scrape <url>` routes
// to the right MatchesURL.
var sites = []SiteConfig{
	{
		ID:       "asiansuckdolls",
		SiteBase: "https://asiansuckdolls.com",
		SiteName: "Asian Suck Dolls",
		Patterns: []string{"asiansuckdolls.com/", "asiansuckdolls.com/videos"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?asiansuckdolls\.com(?:/|$)`),
	},
	{
		ID:       "asiansybian",
		SiteBase: "https://asiansybian.com",
		SiteName: "Asian Sybian",
		Patterns: []string{"asiansybian.com/", "asiansybian.com/videos"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?asiansybian\.com(?:/|$)`),
	},
	{
		ID:       "creamedcuties",
		SiteBase: "https://creamedcuties.com",
		SiteName: "Creamed Cuties",
		Patterns: []string{"creamedcuties.com/", "creamedcuties.com/videos"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?creamedcuties\.com(?:/|$)`),
	},
	{
		ID:       "creampiecuties",
		SiteBase: "https://creampiecuties.com",
		SiteName: "Creampie Cuties",
		Patterns: []string{"creampiecuties.com/", "creampiecuties.com/videos"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?creampiecuties\.com(?:/|$)`),
	},
	{
		ID:       "gogobarauditions",
		SiteBase: "https://gogobarauditions.com",
		SiteName: "Gogo Bar Auditions",
		Patterns: []string{"gogobarauditions.com/", "gogobarauditions.com/videos"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?gogobarauditions\.com(?:/|$)`),
	},
	{
		ID:       "thaigirlswild",
		SiteBase: "https://thaigirlswild.com",
		SiteName: "Thai Girls Wild",
		Patterns: []string{"thaigirlswild.com/", "thaigirlswild.com/videos"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?thaigirlswild\.com(?:/|$)`),
	},
	{
		ID:       "thaipussymassage",
		SiteBase: "https://thaipussymassage.com",
		SiteName: "Thai Pussy Massage",
		Patterns: []string{"thaipussymassage.com/", "thaipussymassage.com/videos"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?thaipussymassage\.com(?:/|$)`),
	},
	{
		ID:       "tussinee",
		SiteBase: "https://tussinee.com",
		SiteName: "Tussinee",
		Patterns: []string{"tussinee.com/", "tussinee.com/videos"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?tussinee\.com(?:/|$)`),
	},
	{
		ID:       "tussineegold",
		SiteBase: "https://tussineegold.com",
		SiteName: "Tussinee Gold",
		Patterns: []string{"tussineegold.com/", "tussineegold.com/videos"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?tussineegold\.com(?:/|$)`),
	},
	{
		ID:         "johnnyrapid",
		SiteBase:   "https://johnnyrapid.com",
		SiteName:   "Johnny Rapid",
		StudioName: "Johnny Rapid",
		Patterns:   []string{"johnnyrapid.com/", "johnnyrapid.com/videos"},
		MatchRe:    regexp.MustCompile(`^https?://(?:www\.)?johnnyrapid\.com(?:/|$)`),
	},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}
