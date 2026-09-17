package globetwatters

import "github.com/Anastylosis/FSS/scraper"

var sites = []SiteConfig{
	{SiteID: "asiansexdiary", Domain: "asiansexdiary.com", StudioName: "Asian Sex Diary"},
	{SiteID: "trikepatrol", Domain: "trikepatrol.com", StudioName: "Trike Patrol"},
	{SiteID: "milftrip", Domain: "milftrip.com", StudioName: "MILF Trip"},
	{SiteID: "tuktukpatrol", Domain: "tuktukpatrol.com", StudioName: "TukTuk Patrol"},
	{SiteID: "helloladyboy", Domain: "helloladyboy.com", StudioName: "Hello Ladyboy"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}
