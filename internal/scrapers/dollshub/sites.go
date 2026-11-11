package dollshub

import "github.com/Anastylosis/FSS/scraper"

var sites = []SiteConfig{
	{SiteID: "maturegapers", Domain: "maturegapers.com", StudioName: "Mature Gapers"},
	{SiteID: "filthygapers", Domain: "filthygapers.com", StudioName: "Filthy Gapers"},
	{SiteID: "gynoexclusive", Domain: "gynoexclusive.com", StudioName: "Gyno Exclusive"},
	{SiteID: "ihuntmycunt", Domain: "ihuntmycunt.com", StudioName: "I Hunt My Cunt"},
	{SiteID: "maturegynoexam", Domain: "maturegynoexam.com", StudioName: "Mature Gyno Exam"},
	{SiteID: "maturegynospy", Domain: "maturegynospy.com", StudioName: "Mature Gyno Spy"},
	{SiteID: "nastypublicsex", Domain: "nastypublicsex.com", StudioName: "Nasty Public Sex"},
	{SiteID: "oldsfuckdolls", Domain: "oldsfuckdolls.com", StudioName: "Olds Fuck Dolls"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}
