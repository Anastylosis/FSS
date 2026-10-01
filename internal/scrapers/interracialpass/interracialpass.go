// Package interracialpass registers the Interracial Pass scraper. The site
// (Hush Hush Entertainment) runs the modern Bootstrap-grid NATS template
// (item-update item-video cards, /trailers/{slug}.html detail pages) served
// under the /t1 tour prefix, so it reuses darkreachmodernutil.
package interracialpass

import (
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/darkreachmodernutil"
	"github.com/Anastylosis/FSS/scraper"
)

func init() {
	scraper.Register(darkreachmodernutil.New(darkreachmodernutil.SiteConfig{
		ID:         "interracialpass",
		SiteBase:   "https://www.interracialpass.com",
		Studio:     "Interracial Pass",
		TourPrefix: "/t1",
		Patterns: []string{
			"interracialpass.com",
			"interracialpass.com/t1/",
			"interracialpass.com/t1/categories/movies_{N}_d.html",
		},
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?interracialpass\.com(?:/|$)`),
	}))
}
