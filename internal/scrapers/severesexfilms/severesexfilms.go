// Package severesexfilms registers severesexfilms.com, which runs the same
// Next.js `__NEXT_DATA__` shop template as the Ghost Pro sister sites and so
// reuses that package's engine. See docs/scrapers.md.
package severesexfilms

import (
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/ghostpro"
	"github.com/Anastylosis/FSS/scraper"
)

var site = ghostpro.SiteConfig{
	ID:         "severesexfilms",
	SiteBase:   "https://www.severesexfilms.com",
	SiteName:   "Severe Sex Films",
	StudioName: "Severe Sex Films",
	Patterns:   []string{"severesexfilms.com/", "severesexfilms.com/videos"},
	MatchRe:    regexp.MustCompile(`^https?://(?:www\.)?severesexfilms\.com(?:/|$)`),
}

func init() { scraper.Register(ghostpro.New(site)) }
