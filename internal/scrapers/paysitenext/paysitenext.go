// Package paysitenext registers the independent studios running the
// Paysite.com Next.js template (see paysiteutil). They share no network — each
// is its own studio that bought the same tour software — so they are grouped
// by template rather than by owner.
package paysitenext

import (
	"github.com/Anastylosis/FSS/internal/scrapers/paysiteutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []paysiteutil.SiteConfig{
	{SiteID: "benefitmonkey", Domain: "benefitmonkey.com", StudioName: "Benefit Monkey", ListPath: "scenes", OnlySiteDomain: "benefitmonkey.com", Aliases: []string{"thebenefitmonkey.com"}},
	{SiteID: "bondagebros", Domain: "bondagebros.com", StudioName: "Bondage Bros"},
	{SiteID: "bjraw", Domain: "bjraw.com", StudioName: "BJ Raw"},
	{SiteID: "dickhddaily", Domain: "dickhddaily.com", StudioName: "DickHDDaily"},
	{SiteID: "dripdropprod", Domain: "dripdropprod.net", StudioName: "DripDrop Productions", ListPath: "scenes"},
	{SiteID: "gentlemenscloset", Domain: "gentlemenscloset.com", StudioName: "Gentlemen's Closet"},
	{SiteID: "gotfilled", Domain: "gotfilled.com", StudioName: "Got Filled"},
	{SiteID: "hardwerk", Domain: "hardwerk.com", StudioName: "HardWerk Pictures", ListPath: "films"},
	{SiteID: "inserted", Domain: "inserted.com", StudioName: "Inserted"},
	{SiteID: "jamieyoung", Domain: "jamie-young.com", StudioName: "Jamie Young"},
	{SiteID: "lezkey", Domain: "lezkey.com", StudioName: "LezKey", ListPath: "scenes"},
	{SiteID: "lucidflix", Domain: "lucidflix.com", StudioName: "LucidFlix", ListPath: "episodes"},
	{SiteID: "lollipoptwinks", Domain: "lollipoptwinks.com", StudioName: "Lollipop Twinks", ListPath: paysiteutil.RootListPath},
	{SiteID: "missmalorieswitch", Domain: "missmalorieswitch.com", StudioName: "Miss Malorie Switch"},
	{SiteID: "nastydaddy", Domain: "nastydaddy.com", StudioName: "Nasty Daddy"},
	{SiteID: "nickmarxx", Domain: "nickmarxx.com", StudioName: "Nick Marxx"},
	{SiteID: "queercrush", Domain: "queercrush.com", StudioName: "QueerCrush"},
	{SiteID: "pervect", Domain: "pervect.com", StudioName: "Pervect", ListPath: "scenes"},
	{SiteID: "phoenixxx", Domain: "phoenixxx.com", StudioName: "Phoenixxx", ListPath: "scenes"},
	{SiteID: "purgatoryx", Domain: "tour.purgatoryx.com", StudioName: "PurgatoryX", Aliases: []string{"purgatoryx.com"}},
	{SiteID: "rickysroom", Domain: "rickysroom.com", StudioName: "Ricky's Room"},
	{SiteID: "s3xus", Domain: "s3xus.com", StudioName: "S3XUS", ListPath: "scenes"},
	// Trailer Trash Boys has no scene pages of its own (a JS-only shell); its
	// scenes are the trailertrashboys.com items in the Ducati Studios network feed.
	{SiteID: "trailertrashboys", Domain: "trailertrashboys.com", FetchDomain: "ducatistudios.com", StudioName: "Trailer Trash Boys", OnlySiteDomain: "trailertrashboys.com", Aliases: []string{"trentonducati.com"}},
	// The whole Ducati Studios feed: the brands with rows of their own
	// (Gentlemen's Closet, Nasty Daddy, Bondage Bros, Trailer Trash Boys) plus Ducati
	// Originals, Daddy Patrick, Johnny Rapid, Edward James and Choice Filmz.
	{SiteID: "ducatistudios", Domain: "ducatistudios.com", StudioName: "Ducati Studios"},
	// Yes Girlz serves the same template one path over: /videos 404s, /scenes
	// is the listing.
	{SiteID: "yesgirlz", Domain: "yesgirlz.com", StudioName: "YesGirlz", ListPath: "scenes"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(paysiteutil.New(cfg))
	}
}
