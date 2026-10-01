package glamose

import (
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/internal/scrapers/utgutil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []utgutil.SiteConfig{
	// Livewire template (<article> cards, /updates/videos?updates_page=N)
	{SiteID: "hayleyssecrets", Domain: "hayleyssecrets.com", StudioName: "Hayley's Secrets"},
	{SiteID: "morethannylons", Domain: "morethannylons.com", StudioName: "More Than Nylons"},
	{SiteID: "skintightglamour", Domain: "skintightglamour.com", StudioName: "Skin Tight Glamour"},
	{SiteID: "uktickling", Domain: "uktickling.com", StudioName: "UK Tickling"},
	{SiteID: "worshipjasmine", Domain: "worshipjasmine.com", StudioName: "Worship Jasmine"},
	{SiteID: "allbrookwright", Domain: "allbrookwright.com", StudioName: "All Brook Wright"},
	{SiteID: "bethmorganofficial", Domain: "bethmorganofficial.com", StudioName: "Beth Morgan Official"},
	{SiteID: "breathtakers", Domain: "breath-takers.com", StudioName: "BreathTakers"},
	{SiteID: "girlfolio", Domain: "girlfolio.com", StudioName: "Girlfolio"},
	// Legacy template (Bootstrap 3, page-based pagination)
	{SiteID: "sophiassexylegwear", Domain: "sophiassexylegwear.com", StudioName: "Sophia's Sexy Legwear", Legacy: true},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(utgutil.New(cfg))
	}
	scraper.Register(&portalScraper{client: httpx.NewClient(30 * time.Second)})
}
