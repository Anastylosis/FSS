// Package lukespov scrapes Lukes POV (lukespov.vip), a MyMember.site instance on its own
// domain. Listing and detail handling live in mymemberutil; this package only
// supplies the site config.
package lukespov

import (
	"context"
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/mymemberutil"
	"github.com/Anastylosis/FSS/scraper"
)

var mm = mymemberutil.New(mymemberutil.SiteConfig{
	SiteID:     "lukespov",
	Domain:     "lukespov.vip",
	StudioName: "Lukes POV",
	KnownPerformers: map[string]bool{
		"luke riggs":       true,
		"gia rouge":        true,
		"katija cortez":    true,
		"hope ryan":        true,
		"liberty jane":     true,
		"coco bae":         true,
		"tayla":            true,
		"kayla lea":        true,
		"tori cummings":    true,
		"stefani marz":     true,
		"isabelle deltore": true,
		"nina milano":      true,
		"jordan millicent": true,
		"magdalene sin":    true,
		"ruby ray":         true,
		"lily thorn":       true,
		"olive may":        true,
		"imogen knox":      true,
		"kayla jade":       true,
		"andy adams":       true,
		"katie cassidy":    true,
		"aeysha":           true,
		"roxie sanchez":    true,
		"fit kitty":        true,
		"mini barbie":      true,
	},
})

// Scraper implements scraper.StudioScraper for Lukes POV.
type Scraper struct {
	mm *mymemberutil.Scraper
}

// New constructs a Lukes POV scraper.
func New() *Scraper { return &Scraper{mm: mm} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string         { return mm.Config().SiteID }
func (s *Scraper) Patterns() []string { return []string{"lukespov.vip"} }

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?lukespov\.vip(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.mm.Run(ctx, studioURL, opts, out)
	}()
	return out, nil
}
