package kissmegirl

import (
	"regexp"

	"github.com/Anastylosis/FSS/scraper"
)

// sites is the GreyWood network: three hand-built static-HTML tours sharing
// one listing template. Only KissMe Girl hosts the model index; the other two
// link back to it.
var sites = []SiteConfig{
	{
		ID:          "kissmegirl",
		Base:        "https://www.kissmegirl.com",
		Studio:      "KissMe Girl",
		FirstPage:   "vod.html",
		PagePattern: "vodpage%d.html",
		ModelPages:  true,
		MatchRe:     regexp.MustCompile(`^https?://(?:www\.)?kissmegirl\.com(?:/|$)`),
	},
	{
		ID:          "kissmegirlexplicit",
		Base:        "https://www.kissmegirlexplicit.com",
		Studio:      "KissMe Girl Explicit",
		FirstPage:   "scenes.html",
		PagePattern: "scenespage%d.html",
		MatchRe:     regexp.MustCompile(`^https?://(?:www\.)?kissmegirlexplicit\.com(?:/|$)`),
	},
	{
		ID:          "sloppygirl",
		Base:        "https://www.sloppygirl.com",
		Studio:      "Sloppy Girl",
		FirstPage:   "scenes.html",
		PagePattern: "scenespage%d.html",
		MatchRe:     regexp.MustCompile(`^https?://(?:www\.)?sloppygirl\.com(?:/|$)`),
	},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}
