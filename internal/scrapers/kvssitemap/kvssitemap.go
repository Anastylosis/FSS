// Package kvssitemap registers the KVS sitemap sites. See docs/scrapers.md.
package kvssitemap

import (
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/kvssitemaputil"
	"github.com/Anastylosis/FSS/scraper"
)

var sites = []kvssitemaputil.SiteConfig{
	{
		SiteID:         "barbisinclair",
		Base:           "https://barbisinclair.com",
		StudioName:     "Barbi Sinclair",
		Filters:        []string{"models", "tags"},
		PerformerRe:    regexp.MustCompile(`(?s)<h3 class="media-box__title">(.*?)</h3>`),
		TagRe:          regexp.MustCompile(`(?s)<a class="btn" href="[^"]*/tags/[^"]*">(.*?)</a>`),
		SkipPerformers: []string{"Lucky Guy"},
	},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(kvssitemaputil.New(cfg))
	}
}
