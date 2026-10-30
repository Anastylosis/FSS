// Package whoaboyz registers the whoaboyz.com scraper: the My Gay Cash NATS
// tour CMS on a self-hosted backend (whoabucks.com). cms_area_id and natsUrl
// come from https://whoaboyz.com/natscms-app/config.json.
package whoaboyz

import (
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/natscmsutil"
	"github.com/Anastylosis/FSS/scraper"
)

func New() *natscmsutil.Scraper {
	return natscmsutil.New(natscmsutil.SiteConfig{
		ID:          "whoaboyz",
		SiteBase:    "https://whoaboyz.com",
		SiteName:    "Whoa Boyz",
		StudioName:  "Whoa Boyz",
		NatsAPIBase: "https://whoabucks.com/tour_api.php",
		CMSAreaID:   "9713561d-384a-47b3-b5cc-999f9b86d135",
		Patterns:    []string{"https://whoaboyz.com/"},
		MatchRe:     regexp.MustCompile(`^https?://(?:www\.)?whoaboyz\.com(?:/|$)`),
	})
}

func init() { scraper.Register(New()) }
