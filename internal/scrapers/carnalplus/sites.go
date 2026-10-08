package carnalplus

import (
	"regexp"

	"github.com/Anastylosis/FSS/scraper"
)

// sites enumerates every Carnal+ network site we cover.
//
//   - 20 NATS sister domains (Variant NATS), 7 of them the Jockpack brands.
//   - 1 grid-item portal (the parent carnalplus.com) — its homepage
//     aggregates scenes from every sub-brand, each card carrying its
//     source brand via the minilogo alt-text which we lift onto
//     `Scene.Series`.
//   - 2 grid-item sub-brands hosted at `/baptistboys/` and
//     `/carnaloriginals/` on the parent (Variant Grid + SubPath).
//   - 1 WordPress site, growlboys.com (Variant WordPress).
//
// Plus the jockpack.com portal and its 3 portal-only brands.
var sites = []SiteConfig{
	// ---- VariantNATS — 20 standalone sister domains ----
	{
		ID: "americanmusclehunks", SiteBase: "https://americanmusclehunks.com",
		SiteName: "American Muscle Hunks", Variant: VariantNATS,
		Patterns: []string{"americanmusclehunks.com/", "americanmusclehunks.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?americanmusclehunks\.com(?:/|$)`),
	},
	{
		ID: "bangbangboys", SiteBase: "https://bangbangboys.com",
		SiteName: "BangBangBoys", Variant: VariantNATS,
		Patterns: []string{"bangbangboys.com/", "bangbangboys.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?bangbangboys\.com(?:/|$)`),
	},
	{
		ID: "boyforsale", SiteBase: "https://boyforsale.com",
		SiteName: "Boy For Sale", Variant: VariantNATS,
		Patterns: []string{"boyforsale.com/", "boyforsale.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?boyforsale\.com(?:/|$)`),
	},
	{
		ID: "catholicboys", SiteBase: "https://catholicboys.com",
		SiteName: "Catholic Boys", Variant: VariantNATS,
		Patterns: []string{"catholicboys.com/", "catholicboys.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?catholicboys\.com(?:/|$)`),
	},
	{
		ID: "funsizeboys", SiteBase: "https://funsizeboys.com",
		SiteName: "Fun-Size Boys", Variant: VariantNATS,
		Patterns: []string{"funsizeboys.com/", "funsizeboys.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?funsizeboys\.com(?:/|$)`),
	},
	{
		ID: "gaycest", SiteBase: "https://gaycest.com",
		SiteName: "Gaycest", Variant: VariantNATS,
		Patterns: []string{"gaycest.com/", "gaycest.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?gaycest\.com(?:/|$)`),
	},
	{
		ID: "jalifstudio", SiteBase: "https://jalifstudio.com",
		SiteName: "Jalif Studio", Variant: VariantNATS,
		Patterns: []string{"jalifstudio.com/", "jalifstudio.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?jalifstudio\.com(?:/|$)`),
	},
	{
		ID: "masonicboys", SiteBase: "https://masonicboys.com",
		SiteName: "Masonic Boys", Variant: VariantNATS,
		Patterns: []string{"masonicboys.com/", "masonicboys.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?masonicboys\.com(?:/|$)`),
	},
	{
		ID: "scoutboys", SiteBase: "https://scoutboys.com",
		SiteName: "Scout Boys", Variant: VariantNATS,
		Patterns: []string{"scoutboys.com/", "scoutboys.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?scoutboys\.com(?:/|$)`),
	},
	{
		ID: "staghomme", SiteBase: "https://staghomme.com",
		SiteName: "Stag Homme", Variant: VariantNATS,
		Patterns: []string{"staghomme.com/", "staghomme.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?staghomme\.com(?:/|$)`),
	},
	{
		ID: "teensandtwinks", SiteBase: "https://teensandtwinks.com",
		SiteName: "TeensAndTwinks", Variant: VariantNATS,
		Patterns: []string{"teensandtwinks.com/", "teensandtwinks.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?teensandtwinks\.com(?:/|$)`),
	},
	{
		ID: "twinktop", SiteBase: "https://twinktop.com",
		SiteName: "Twink Top", Variant: VariantNATS,
		Patterns: []string{"twinktop.com/", "twinktop.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?twinktop\.com(?:/|$)`),
	},
	{
		ID: "twinks", SiteBase: "https://twinks.com",
		SiteName: "Twinks", Variant: VariantNATS,
		Patterns: []string{"twinks.com/", "twinks.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?twinks\.com(?:/|$)`),
	},
	{
		ID: "boundtwinks", SiteBase: "https://boundtwinks.com",
		SiteName: "BoundTwinks", Variant: VariantNATS,
		Patterns: []string{"boundtwinks.com/", "boundtwinks.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?boundtwinks\.com(?:/|$)`),
	},
	{
		ID: "cumdumpsluts", SiteBase: "https://cumdumpsluts.com",
		SiteName: "Cum Dump Sluts", Variant: VariantNATS,
		Patterns: []string{"cumdumpsluts.com/", "cumdumpsluts.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?cumdumpsluts\.com(?:/|$)`),
	},
	{
		ID: "dirtyboysociety", SiteBase: "https://dirtyboysociety.com",
		SiteName: "Dirty Boy Society", Variant: VariantNATS,
		Patterns: []string{"dirtyboysociety.com/", "dirtyboysociety.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?dirtyboysociety\.com(?:/|$)`),
	},
	{
		ID: "hungfuckers", SiteBase: "https://hungfuckers.com",
		SiteName: "Hung Fuckers", Variant: VariantNATS,
		Patterns: []string{"hungfuckers.com/", "hungfuckers.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?hungfuckers\.com(?:/|$)`),
	},
	{
		ID: "jockbreeders", SiteBase: "https://jockbreeders.com",
		SiteName: "JockBreeders", Variant: VariantNATS,
		Patterns: []string{"jockbreeders.com/", "jockbreeders.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?jockbreeders\.com(?:/|$)`),
	},
	{
		ID: "rawfuckboys", SiteBase: "https://rawfuckboys.com",
		SiteName: "Raw Fuck Boys", Variant: VariantNATS,
		Patterns: []string{"rawfuckboys.com/", "rawfuckboys.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?rawfuckboys\.com(?:/|$)`),
	},
	{
		ID: "twinkloads", SiteBase: "https://twinkloads.com",
		SiteName: "TwinkLoads", Variant: VariantNATS,
		Patterns: []string{"twinkloads.com/", "twinkloads.com/categories/movies_{N}_d.html"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?twinkloads\.com(?:/|$)`),
	},

	// ---- VariantGrid — the carnalplus.com portal + 2 sub-brand paths ----
	{
		// The parent portal — aggregates scenes from every sub-brand;
		// `Scene.Series` is set per-card from the minilogo alt-text so
		// the parent pass tags each scene with its true sub-site.
		// MatchRe deliberately rejects sub-paths so the baptistboys /
		// carnaloriginals scrapers below own those routes instead.
		ID: "carnalplus", SiteBase: "https://carnalplus.com",
		SiteName: "Carnal+", Variant: VariantGrid,
		Patterns: []string{"carnalplus.com/", "carnalplus.com/?page={N}"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?carnalplus\.com(?:/?$|/?\?)`),
	},
	{
		// Baptist Boys — lives only on the parent at /baptistboys/.
		ID: "baptistboys", SiteBase: "https://carnalplus.com", SubPath: "/baptistboys",
		SiteName: "Baptist Boys", Variant: VariantGrid,
		Patterns: []string{"carnalplus.com/baptistboys/", "carnalplus.com/baptistboys/?page={N}"},
		// MatchRe rejects bare carnalplus.com because the parent scraper
		// owns that; we only fire on the exact sub-path.
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?carnalplus\.com/baptistboys(?:/|$|\?)`),
	},
	{
		ID: "carnaloriginals", SiteBase: "https://carnalplus.com", SubPath: "/carnaloriginals",
		SiteName: "Carnal+ Originals", Variant: VariantGrid,
		Patterns: []string{"carnalplus.com/carnaloriginals/", "carnalplus.com/carnaloriginals/?page={N}"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?carnalplus\.com/carnaloriginals(?:/|$|\?)`),
	},

	// ---- jockpack.com portal — aggregates 10 brands, 3 of which exist only here ----
	{
		ID: "jockpack", SiteBase: "https://jockpack.com", ListPath: "/videos",
		SiteName: "Jockpack", Variant: VariantGrid,
		Patterns:  []string{"jockpack.com/", "jockpack.com/videos?page={N}", "jockpack.com/videos?site={brand}"},
		MatchRe:   regexp.MustCompile(`^https?://(?:www\.)?jockpack\.com(?:/?$|/?\?|/videos/?(?:$|\?))`),
		ExcludeRe: regexp.MustCompile(`[?&]site=(?:betabetapi|studloads|jasonsparkslive)(?:&|$)`),
	},
	{
		ID: "betabetapi", SiteBase: "https://jockpack.com", ListPath: "/videos", SiteFilter: "betabetapi",
		SiteName: "BetaBetaPi", Variant: VariantGrid,
		Patterns: []string{"jockpack.com/videos?site=betabetapi", "jockpack.com/videos?site=betabetapi&page={N}"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?jockpack\.com/videos/?\?(?:[^#]*&)?site=betabetapi(?:&|$)`),
	},
	{
		ID: "studloads", SiteBase: "https://jockpack.com", ListPath: "/videos", SiteFilter: "studloads",
		SiteName: "StudLoads", Variant: VariantGrid,
		Patterns: []string{"jockpack.com/videos?site=studloads", "jockpack.com/videos?site=studloads&page={N}"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?jockpack\.com/videos/?\?(?:[^#]*&)?site=studloads(?:&|$)`),
	},
	{
		ID: "jasonsparkslive", SiteBase: "https://jockpack.com", ListPath: "/videos", SiteFilter: "jasonsparkslive",
		SiteName: "Jason Sparks Live", Variant: VariantGrid,
		Patterns: []string{"jockpack.com/videos?site=jasonsparkslive", "jockpack.com/videos?site=jasonsparkslive&page={N}"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?jockpack\.com/videos/?\?(?:[^#]*&)?site=jasonsparkslive(?:&|$)`),
	},

	// ---- VariantWordPress — GrowlBoys ----
	{
		ID: "growlboys", SiteBase: "https://growlboys.com",
		SiteName: "GrowlBoys", Variant: VariantWordPress,
		Patterns: []string{"growlboys.com/", "growlboys.com/wp-json/wp/v2/posts"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?growlboys\.com(?:/|$)`),
	},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}
