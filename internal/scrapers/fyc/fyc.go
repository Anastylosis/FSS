package fyc

import (
	"github.com/Anastylosis/FSS/internal/scrapers/fycutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []fycutil.SiteConfig{
	{SiteID: "passionhd", Domain: "passion-hd.com", StudioName: "Passion HD"},
	{SiteID: "tiny4k", Domain: "tiny4k.com", StudioName: "Tiny4K"},
	{SiteID: "povd", Domain: "povd.com", StudioName: "POVD"},
	{SiteID: "castingcouchx", Domain: "castingcouch-x.com", StudioName: "Casting Couch X"},
	{SiteID: "lubed", Domain: "lubed.com", StudioName: "Lubed"},
	{SiteID: "spyfam", Domain: "spyfam.com", StudioName: "SpyFam"},
	{SiteID: "cum4k", Domain: "cum4k.com", StudioName: "Cum4K"},
	{SiteID: "holed", Domain: "holed.com", StudioName: "Holed"},
	{SiteID: "girlcum", Domain: "girlcum.com", StudioName: "GirlCum"},
	{SiteID: "exotic4k", Domain: "exotic4k.com", StudioName: "Exotic4K"},
	{SiteID: "wetvr", Domain: "wetvr.com", StudioName: "WetVR"},
	{SiteID: "bbcpie", Domain: "bbcpie.com", StudioName: "BBC Pie"},
	{SiteID: "fantasyhd", Domain: "fantasyhd.com", StudioName: "Fantasy HD"},
	{SiteID: "facials4k", Domain: "facials4k.com", StudioName: "Facials4K"},
	{SiteID: "myveryfirsttime", Domain: "myveryfirsttime.com", StudioName: "My Very First Time"},
	{SiteID: "anal4k", Domain: "anal4k.com", StudioName: "Anal 4K"},
	{SiteID: "nannyspy", Domain: "nannyspy.com", StudioName: "NannySpy"},
	{SiteID: "strippers4k", Domain: "strippers4k.com", StudioName: "Strippers4K"},
	{SiteID: "mom4k", Domain: "mom4k.com", StudioName: "Mom 4K"},
	{SiteID: "pornpros", Domain: "pornpros.com", StudioName: "Porn Pros"},
	{SiteID: "baeb", Domain: "baeb.com", StudioName: "Baeb"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(fycutil.New(cfg))
	}
}
