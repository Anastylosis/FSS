package vna

import (
	"github.com/Anastylosis/FSS/internal/scrapers/vnautil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []vnautil.SiteConfig{
	{SiteID: "allanalallthetime", Domain: "allanalallthetime.com", Studio: "All Anal All The Time", VideoPrefix: "videos"},
	{SiteID: "angelinacastro", Domain: "angelinacastrolive.com", Studio: "Angelina Castro", VideoPrefix: "videos"},
	{SiteID: "blownbyrone", Domain: "blownbyrone.com", Studio: "Blown By Rone", VideoPrefix: "videos"},
	{SiteID: "carmenvalentina", Domain: "carmenvalentina.com", Studio: "Carmen Valentina", VideoPrefix: "videos"},
	{SiteID: "cristiann", Domain: "cristiannlive.com", Studio: "CristiAnnLive", VideoPrefix: "videos"},
	{SiteID: "deauxma", Domain: "deauxmalive.com", Studio: "Deauxma Live", VideoPrefix: "videoset"},
	{SiteID: "foxxedup", Domain: "foxxedup.com", Studio: "FoXXedUp", VideoPrefix: "videos"},
	{SiteID: "fuckedfeet", Domain: "fuckedfeet.com", Studio: "Fucked Feet", VideoPrefix: "videos"},
	{SiteID: "girlgirlmania", Domain: "girlgirlmania.com", Studio: "Girl Girl Mania", VideoPrefix: "videos"},
	{SiteID: "itscleo", Domain: "itscleolive.com", Studio: "It's Cleo Live", VideoPrefix: "videos", NeedsWWW: true},
	{SiteID: "jelenajensen", Domain: "jelenajensen.com", Studio: "Jelena Jensen", VideoPrefix: "videos"},
	{SiteID: "juliaann", Domain: "juliaannlive.com", Studio: "Julia Ann Live", VideoPrefix: "videos"},
	{SiteID: "kimberlee", Domain: "kimberleelive.com", Studio: "Kimber Lee Live", VideoPrefix: "videos"},
	{SiteID: "kink305", Domain: "kink305.com", Studio: "Kink305", VideoPrefix: "videos"},
	{SiteID: "maggiegreen", Domain: "maggiegreenlive.com", Studio: "Maggie Green Live", VideoPrefix: "videos"},
	{SiteID: "majorhotwife", Domain: "majorhotwife.com", Studio: "Major Hotwife", VideoPrefix: "videos"},
	{SiteID: "nataliastarr", Domain: "nataliastarr.com", Studio: "Natalia Starr", VideoPrefix: "videos"},
	{SiteID: "ninakayy", Domain: "ninakayy.com", Studio: "Nina Kayy", VideoPrefix: "videos"},
	{SiteID: "pennypax", Domain: "pennypaxlive.com", Studio: "Penny Pax Live", VideoPrefix: "videos"},
	{SiteID: "povmania", Domain: "povmania.com", Studio: "POV Mania", VideoPrefix: "videos"},
	{SiteID: "romemajor", Domain: "romemajor.com", Studio: "Rome Major", VideoPrefix: "videos"},
	{SiteID: "rubberdoll", Domain: "rubberdoll.net", Studio: "RubberDoll", VideoPrefix: "videos", NeedsWWW: true},
	{SiteID: "sarajay", Domain: "sarajay.com", Studio: "Sara Jay", VideoPrefix: "videos"},
	{SiteID: "shandafay", Domain: "shandafay.com", Studio: "Shanda Fay", VideoPrefix: "videos"},
	{SiteID: "siripornstar", Domain: "siripornstar.com", Studio: "Siri Pornstar", VideoPrefix: "videos"},
	{SiteID: "sunnylane", Domain: "sunnylanelive.com", Studio: "Sunny Lane", VideoPrefix: "videos"},
	{SiteID: "vickyathome", Domain: "vickyathome.com", Studio: "Vicky at Home", VideoPrefix: "milf-videos"},
	{SiteID: "womenbyjuliaann", Domain: "womenbyjuliaann.com", Studio: "Women By Julia Ann", VideoPrefix: "videos"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(vnautil.New(cfg))
	}
}
