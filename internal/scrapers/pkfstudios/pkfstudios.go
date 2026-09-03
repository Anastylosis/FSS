// Package pkfstudios registers the PKF Studios scraper. The catalogue is a
// WordPress blog at `/updates/` with an open REST API — the apex is a
// hand-written age-gate page with no content of its own — so the SiteBase
// carries that subpath. Scenes are `post` objects; see fotoroutil.
package pkfstudios

import (
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/fotoroutil"
	"github.com/Anastylosis/FSS/scraper"
)

func init() {
	scraper.Register(fotoroutil.New(fotoroutil.SiteConfig{
		ID:         "pkfstudios",
		Studio:     "PKF Studios",
		SiteBase:   "https://www.pkfstudios.com/updates",
		TagsAsTags: true,
		Patterns:   []string{"pkfstudios.com", "pkfstudios.com/updates/"},
		MatchRe:    regexp.MustCompile(`^https?://(?:www\.)?pkfstudios\.com(?:/|$)`),
	}))
}
