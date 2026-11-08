package grooby

import (
	"github.com/Anastylosis/FSS/internal/scrapers/groobyutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []groobyutil.SiteConfig{
	{SiteID: "asianamericantgirls", Domain: "asianamericantgirls.com", StudioName: "Asian American TGirls", TourPrefix: "/tour"},
	{SiteID: "asiantgirl", Domain: "asiantgirl.com", StudioName: "Asian Tgirl", TourPrefix: "/tour"},
	{SiteID: "blacktgirls", Domain: "black-tgirls.com", StudioName: "Black TGirls", TourPrefix: "/tour"},
	{SiteID: "blacktgirlshardcore", Domain: "blacktgirlshardcore.com", StudioName: "Black TGirls Hardcore", TourPrefix: "/tour"},
	{SiteID: "bobstgirls", Domain: "bobstgirls.com", StudioName: "Bob's TGirls", TourPrefix: "/tour"},
	{SiteID: "braziliantranssexuals", Domain: "brazilian-transsexuals.com", StudioName: "Brazilian Transsexuals", TourPrefix: "/tour"},
	{SiteID: "braziltgirls", Domain: "braziltgirls.xxx", StudioName: "Brazil TGirls", TourPrefix: "/tour"},
	{SiteID: "canadatgirl", Domain: "canada-tgirl.com", StudioName: "Canada TGirl", TourPrefix: "/tour"},
	// TransErotica performer sub-tours: same CMS and billing, own catalogue at
	// the document root. Only the two that still serve one are registered —
	// cherrymavrik carries 4 scenes the main tour does not. The other 15
	// StashDB children are parked, personal bio pages, or (marissaminx) render
	// every card as an affiliate redirect with no scene URL to store.
	{SiteID: "cherrymavrik", Domain: "cherrymavrik.com", StudioName: "Cherry Mavrik"},
	{SiteID: "kayleighcoxx", Domain: "kayleighcoxx.com", StudioName: "Kayleigh Coxx"},
	{SiteID: "eurotgirls", Domain: "euro-tgirls.com", StudioName: "Euro Tgirls", TourPrefix: "/tour"},
	{SiteID: "femout", Domain: "femout.xxx", StudioName: "Femout.xxx", TourPrefix: "/tour"},
	{SiteID: "femoutsex", Domain: "femoutsex.xxx", StudioName: "Femoutsex.xxx", TourPrefix: "/tour"},
	{SiteID: "frankstgirlworld", Domain: "franks-tgirlworld.com", StudioName: "Frank's TGirl World", TourPrefix: "/tour"},
	{SiteID: "groobyarchives", Domain: "grooby-archives.com", StudioName: "Grooby Archives", TourPrefix: "/tour"},
	{SiteID: "groobygirls", Domain: "groobygirls.com", StudioName: "Grooby Girls", TourPrefix: "/tour", AltDomains: []string{"shemaleyum.com"}},
	{SiteID: "groobyvr", Domain: "groobyvr.com", StudioName: "Grooby VR", TourPrefix: "/tour", AltDomains: []string{"justvr.xxx"}},
	{SiteID: "groobydvd", Domain: "groobydvd.com", StudioName: "GroobyDVD", TourPrefix: "/tour"},
	{SiteID: "krissy4u", Domain: "krissy4u.com", StudioName: "Krissy4U", TourPrefix: "/tour"},
	{SiteID: "ladyboyladyboy", Domain: "ladyboy-ladyboy.com", StudioName: "Ladyboy Ladyboy", TourPrefix: "/tour"},
	{SiteID: "ladyboy", Domain: "ladyboy.xxx", StudioName: "Ladyboy.xxx", TourPrefix: "/tour"},
	{SiteID: "realtgirls", Domain: "realtgirls.com", StudioName: "Real TGirls", TourPrefix: "/tour"},
	{SiteID: "naughtynatural", Domain: "naughtynatural.com", StudioName: "Naughty Natural", TourPrefix: "/tour"},
	{SiteID: "russiantgirls", Domain: "russian-tgirls.com", StudioName: "Russian TGirls", TourPrefix: "/tour"},
	{SiteID: "tgirl40", Domain: "tgirl40.com", StudioName: "TGirl 40", TourPrefix: "/tour"},
	{SiteID: "tgirlbbw", Domain: "tgirlbbw.com", StudioName: "TGirl BBW", TourPrefix: "/tour"},
	{SiteID: "tgirljapan", Domain: "tgirljapan.com", StudioName: "TGirl Japan", TourPrefix: "/tour"},
	{SiteID: "tgirljapanhardcore", Domain: "tgirljapanhardcore.com", StudioName: "TGirl Japan Hardcore", TourPrefix: "/tour"},
	{SiteID: "tgirlpornstar", Domain: "tgirlpornstar.com", StudioName: "TGirl Pornstar", TourPrefix: "/tour"},
	{SiteID: "tgirlpostop", Domain: "tgirlpostop.com", StudioName: "Tgirl Post-Op", TourPrefix: "/tour"},
	{SiteID: "tgirlsex", Domain: "tgirlsex.xxx", StudioName: "tgirlsex.xxx", TourPrefix: "/tour"},
	{SiteID: "tgirlsfuck", Domain: "tgirlsfuck.com", StudioName: "TGirls Fuck", TourPrefix: "/tour"},
	{SiteID: "tgirlshookup", Domain: "tgirlshookup.com", StudioName: "TGirls Hookup", TourPrefix: "/tour"},
	{SiteID: "tgirlsporn", Domain: "tgirls.porn", StudioName: "Tgirls.porn", TourPrefix: "/tour"},
	{SiteID: "tgirlsxxx", Domain: "tgirls.xxx", StudioName: "TGirls.xxx", TourPrefix: "/tour", AltDomains: []string{"shemale.xxx"}},
	{SiteID: "tgirltops", Domain: "tgirltops.com", StudioName: "TGirl Tops", TourPrefix: "/tour"},
	{SiteID: "tgirlx", Domain: "tgirlx.com", StudioName: "TGirl X", TourPrefix: "/tour"},
	{SiteID: "tporn", Domain: "t.porn", StudioName: "T.Porn", TourPrefix: "/tour"},
	{SiteID: "transexdomination", Domain: "transexdomination.com", StudioName: "Transex Domination", TourPrefix: "/tour"},
	{SiteID: "transerotica", Domain: "tour.transerotica.com", StudioName: "TransErotica", AltDomains: []string{"transerotica.com"}},
	{SiteID: "transexpov", Domain: "transexpov.com", StudioName: "Transex POV", TourPrefix: "/tour"},
	{SiteID: "transgasm", Domain: "transgasm.com", StudioName: "Transgasm", TourPrefix: "/tour"},
	{SiteID: "transnificent", Domain: "transnificent.com", StudioName: "Transnificent", TourPrefix: "/tour"},
	{SiteID: "tscastingcouch", Domain: "ts-castingcouch.com", StudioName: "TS Casting Couch", TourPrefix: "/tour"},
	{SiteID: "uktgirls", Domain: "uk-tgirls.com", StudioName: "UK TGirls", TourPrefix: "/tour"},
	{SiteID: "thirdsexxxx", Domain: "thirdsexxxx.com", StudioName: "Third Sex XXX"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(groobyutil.New(cfg))
	}
}
