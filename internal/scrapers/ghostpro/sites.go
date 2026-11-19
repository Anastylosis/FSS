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
		ID:         "johnnyrapid",
		SiteBase:   "https://johnnyrapid.com",
		SiteName:   "Johnny Rapid",
		StudioName: "Johnny Rapid",
		Patterns:   []string{"johnnyrapid.com/", "johnnyrapid.com/videos"},
		MatchRe:    regexp.MustCompile(`^https?://(?:www\.)?johnnyrapid\.com(?:/|$)`),
	},
	{
		ID:         "legendaryx",
		SiteBase:   "https://legendaryx.com",
		SiteName:   "LegendaryX",
		StudioName: "LegendaryX",
		Patterns:   []string{"legendaryx.com/", "legendaryx.com/videos"},
		MatchRe:    regexp.MustCompile(`^https?://(?:www\.)?legendaryx\.com(?:/|$)`),
	},
	{
		ID:         "amazingfilms",
		SiteBase:   "https://amazingfilms.com",
		SiteName:   "Amazing Films",
		StudioName: "Amazing Films",
		Patterns:   []string{"amazingfilms.com/", "amazingfilms.com/videos"},
		MatchRe:    regexp.MustCompile(`^https?://(?:www\.)?amazingfilms\.com(?:/|$)`),
	},
	{
		ID:         "collectivecorruption",
		SiteBase:   "https://collectivecorruption.com",
		SiteName:   "Collective Corruption",
		StudioName: "Collective Corruption",
		Patterns:   []string{"collectivecorruption.com/", "collectivecorruption.com/videos"},
		MatchRe:    regexp.MustCompile(`^https?://(?:www\.)?collectivecorruption\.com(?:/|$)`),
	},
	{
		ID:         "divinedd",
		SiteBase:   "https://divine-dd.com",
		SiteName:   "Divine-DD",
		StudioName: "Divine-DD",
		Patterns:   []string{"divine-dd.com/", "divine-dd.com/videos"},
		MatchRe:    regexp.MustCompile(`^https?://(?:www\.)?divine-dd\.com(?:/|$)`),
	},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}
