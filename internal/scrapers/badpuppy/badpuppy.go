// Package badpuppy registers BadPuppy (badpuppy.com). It runs the same NATS
// CMS as the Mars Media sites but on its own PuppyCash backend, which is why
// it is a package of its own rather than a row in that table.
package badpuppy

import (
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/natscmsutil"
	"github.com/Anastylosis/FSS/scraper"
)

// site is read from badpuppy.com/natscms-app/config.json: `natsUrl` plus
// `/tour_api.php` is the API base, and `cms_area_id` scopes it to this site.
// Sending the area id to Mars Media's backend answers "Invalid Area".
var site = natscmsutil.SiteConfig{
	ID:          "badpuppy",
	SiteBase:    "https://www.badpuppy.com",
	SiteName:    "BadPuppy",
	StudioName:  "BadPuppy",
	NatsAPIBase: "https://nats.puppycash.com/tour_api.php",
	CMSAreaID:   "6f4ac1b5-6e47-47b2-96c7-b8e0af0fe41d",
	Patterns:    []string{"https://www.badpuppy.com/"},
	MatchRe:     regexp.MustCompile(`^https?://(?:www\.|tour\.)?badpuppy\.com(?:/|$)`),
}

func init() { scraper.Register(natscmsutil.New(site)) }
