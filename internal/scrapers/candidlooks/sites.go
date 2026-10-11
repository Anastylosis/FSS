package candidlooks

import (
	"regexp"

	"github.com/Anastylosis/FSS/scraper"
)

var sites = []SiteConfig{
	{ID: "amateurlapdancer", Host: "amateurlapdancer.com", Name: "Amateur Lapdancer"},
	{ID: "onlyfacials", Host: "onlyfacials.com", Name: "OnlyFacials"},
	{ID: "genuinefeatures", Host: "genuinefeatures.com", Name: "Genuine Features"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

func (c SiteConfig) matchRe() *regexp.Regexp {
	return regexp.MustCompile(`(?i)^https?://(?:www\.)?` + regexp.QuoteMeta(c.Host) + `(?:/|$)`)
}
