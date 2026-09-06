// Package hushpass registers the Hush Pass scraper. Like its sibling
// Interracial Pass it is a Hush Hush Entertainment site on the modern
// Bootstrap-grid NATS template under a /t1 tour prefix, so it reuses
// darkreachmodernutil.
//
// The StashDB tree's other members are not separate sites: Bubble Butt
// Bonanza, Milf Invaders and Shot Her First are categories here, and
// abominableblackman.com, biggathananigga.com, frathousefuckfest.com,
// mydaughtersfuckingablackdude.com, mymomsfuckingblackzilla.com and
// whitezilla.com all redirect — three to this site and three to
// interracialpass.com, which fss already covers.
package hushpass

import (
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/darkreachmodernutil"
	"github.com/Anastylosis/FSS/scraper"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?hushpass\.com(?:/|$)`)

func init() {
	scraper.Register(darkreachmodernutil.New(darkreachmodernutil.SiteConfig{
		ID:         "hushpass",
		SiteBase:   "https://hushpass.com",
		Studio:     "Hush Pass",
		TourPrefix: "/t1",
		Patterns: []string{
			"hushpass.com",
			"hushpass.com/t1/categories/movies_{N}_d.html",
		},
		MatchRe: matchRe,
	}))
}
