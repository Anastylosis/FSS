package fundorado

import (
	"github.com/Anastylosis/FSS/internal/scrapers/fundoradoutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []fundoradoutil.SiteConfig{
	{SiteID: "orgasmabuse", Host: "orgasmabuse.com", StudioName: "Orgasm Abuse", Labels: []int{610}},
	{SiteID: "rfmovies", Host: "rfmovies.com", StudioName: "RF Studio", Labels: []int{695}},
	{SiteID: "rubberempire", Host: "rubber-empire.com", StudioName: "Rubber Empire", Labels: []int{749, 751}},
	{SiteID: "texasbukkake", Host: "texasbukkake.com", StudioName: "Texas Bukkake", Labels: []int{622, 642, 679, 620, 644, 632}},
	{SiteID: "taworship", Host: "taworship.com", StudioName: "TA Worship", Labels: []int{614, 612}},
	{SiteID: "tickleabuse", Host: "tickleabuse.com", StudioName: "Tickle Abuse", Labels: []int{616, 618}},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(fundoradoutil.New(cfg))
	}
}
