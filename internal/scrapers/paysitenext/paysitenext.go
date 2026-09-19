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
	{SiteID: "bjraw", Domain: "bjraw.com", StudioName: "BJ Raw"},
	{SiteID: "dickhddaily", Domain: "dickhddaily.com", StudioName: "DickHDDaily"},
	{SiteID: "gotfilled", Domain: "gotfilled.com", StudioName: "Got Filled"},
	{SiteID: "inserted", Domain: "inserted.com", StudioName: "Inserted"},
	{SiteID: "nickmarxx", Domain: "nickmarxx.com", StudioName: "Nick Marxx"},
	{SiteID: "queercrush", Domain: "queercrush.com", StudioName: "QueerCrush"},
	{SiteID: "pervect", Domain: "pervect.com", StudioName: "Pervect", ListPath: "scenes"},
	{SiteID: "purgatoryx", Domain: "tour.purgatoryx.com", StudioName: "PurgatoryX", Aliases: []string{"purgatoryx.com"}},
	{SiteID: "rickysroom", Domain: "rickysroom.com", StudioName: "Ricky's Room"},
	{SiteID: "s3xus", Domain: "s3xus.com", StudioName: "S3XUS", ListPath: "scenes"},
	// Yes Girlz serves the same template one path over: /videos 404s, /scenes
	// is the listing.
	{SiteID: "yesgirlz", Domain: "yesgirlz.com", StudioName: "YesGirlz", ListPath: "scenes"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(paysiteutil.New(cfg))
	}
}
