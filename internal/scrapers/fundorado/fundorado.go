package fundorado

import (
	"github.com/Anastylosis/FSS/internal/scrapers/fundoradoutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []fundoradoutil.SiteConfig{
	{SiteID: "rubberempire", Host: "rubber-empire.com", StudioName: "Rubber Empire", Labels: []int{749, 751}},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(fundoradoutil.New(cfg))
	}
}
