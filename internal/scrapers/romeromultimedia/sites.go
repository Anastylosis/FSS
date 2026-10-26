package romeromultimedia

import (
	"regexp"

	"github.com/Anastylosis/FSS/scraper"
)

// sites enumerates every Romero Multimedia sister site we can reach via WP
// REST API. The footfetish.center entry from stashdb is omitted because its
// `/wp-json/wp/v2/posts` endpoint returns HTTP 404; the Twinz entry uses a
// taxonomy filter against hentaied.pro since Twinz never got its own
// domain.
var sites = []SiteConfig{
	{
		ID:       "cumflation",
		SiteBase: "https://cumflation.com",
		SiteName: "Cumflation",
		Patterns: []string{"cumflation.com/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?cumflation\.com(?:/|$)`),
	},
	{
		ID:       "defeated",
		SiteBase: "https://defeated.xxx",
		SiteName: "Defeated",
		Patterns: []string{"defeated.xxx/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?defeated\.xxx(?:/|$)`),
	},
	{
		ID:       "defeatedsexfight",
		SiteBase: "https://defeatedsexfight.com",
		SiteName: "Defeated Sex Fight",
		Patterns: []string{"defeatedsexfight.com/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?defeatedsexfight\.com(?:/|$)`),
	},
	{
		ID:       "freeze",
		SiteBase: "https://freeze.xxx",
		SiteName: "Freeze",
		Patterns: []string{"freeze.xxx/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?freeze\.xxx(?:/|$)`),
	},
	{
		ID:       "futanarixxx",
		SiteBase: "https://futanari.xxx",
		SiteName: "Futanari XXX",
		Patterns: []string{"futanari.xxx/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?futanari\.xxx(?:/|$)`),
	},
	{
		ID:       "hentaimovie",
		SiteBase: "https://hentai.movie",
		SiteName: "Hentai Movie",
		Patterns: []string{"hentai.movie/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?hentai\.movie(?:/|$)`),
	},
	{
		ID:       "hentaied",
		SiteBase: "https://hentaied.com",
		SiteName: "Hentaied",
		Patterns: []string{"hentaied.com/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?hentaied\.com(?:/|$)`),
	},
	{
		ID:       "monsterporn",
		SiteBase: "https://monsterporn.com",
		SiteName: "MonsterPorn",
		Patterns: []string{"monsterporn.com/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?monsterporn\.com(?:/|$)`),
	},
	{
		ID:       "parasited",
		SiteBase: "https://parasited.com",
		SiteName: "Parasited",
		Patterns: []string{"parasited.com/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?parasited\.com(?:/|$)`),
	},
	{
		ID:       "plantsvscunts",
		SiteBase: "https://plantsvscunts.com",
		SiteName: "Plants vs Cunts",
		Patterns: []string{"plantsvscunts.com/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?plantsvscunts\.com(?:/|$)`),
	},
	{
		ID:       "smokinghawt",
		SiteBase: "https://smokinghawt.com",
		SiteName: "Smoking Hawt",
		Patterns: []string{"smokinghawt.com/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?smokinghawt\.com(?:/|$)`),
	},
	{
		ID:       "somegore",
		SiteBase: "https://somegore.com",
		SiteName: "Somegore",
		Patterns: []string{"somegore.com/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?somegore\.com(?:/|$)`),
	},
	{
		// Twinz never got its own domain — its catalogue lives on the
		// hentaied.pro membership portal, filtered by the `origin_website`
		// taxonomy term ID 411. The stashdb entry's URL
		// (`hentaied.pro/projects/twinz/`) points at the portal's
		// taxonomy-archive page, which is the same content shaped as
		// HTML; we go through the JSON REST endpoint instead.
		ID:              "twinz",
		SiteBase:        "https://hentaied.pro",
		SiteName:        "Twinz",
		OriginWebsiteID: 411,
		Patterns:        []string{"hentaied.pro/projects/twinz/"},
		MatchRe:         regexp.MustCompile(`^https?://(?:www\.)?hentaied\.pro/projects/twinz/?`),
	},
	{
		ID:       "vampired",
		SiteBase: "https://vampired.com",
		SiteName: "Vampired",
		Patterns: []string{"vampired.com/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?vampired\.com(?:/|$)`),
	},
	{
		ID:       "voodooed",
		SiteBase: "https://voodooed.com",
		SiteName: "Voodooed",
		Patterns: []string{"voodooed.com/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?voodooed\.com(?:/|$)`),
	},
	{
		ID:       "vored",
		SiteBase: "https://vored.com",
		SiteName: "VORED",
		Patterns: []string{"vored.com/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?vored\.com(?:/|$)`),
	},
	// Not a Romero site: Anatomik Media publishes its scenes as plain WP REST
	// posts the same way, so it reuses this parser.
	{
		ID:       "anatomikmedia",
		SiteBase: "https://anatomikmedia.com",
		SiteName: "Anatomik Media",
		Studio:   "Anatomik Media",
		Patterns: []string{"anatomikmedia.com/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?anatomikmedia\.com(?:/|$)`),
	},
	// Not a Romero site either: Chica's Place is a WordPress blog under
	// /V2.0/ whose posts are the updates, tagged with keywords.
	{
		ID:          "chicasplace",
		SiteBase:    "http://www.chicasplace.com/V2.0",
		SiteName:    "Chica's Place",
		Studio:      "Chica's Place",
		KeywordTags: true,
		Patterns:    []string{"chicasplace.com/"},
		MatchRe:     regexp.MustCompile(`^https?://(?:www\.)?chicasplace\.com(?:/|$)`),
	},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}
