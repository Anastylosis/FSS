// Package latexlolanoir scrapes Lola Noir (latexlolanoir.com), a MyMember.site instance on its own
// domain. Listing and detail handling live in mymemberutil; this package only
// supplies the site config.
package latexlolanoir

import (
	"context"
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/mymemberutil"
	"github.com/Anastylosis/FSS/scraper"
)

var mm = mymemberutil.New(mymemberutil.SiteConfig{
	SiteID:     "latexlolanoir",
	Domain:     "latexlolanoir.com",
	StudioName: "Lola Noir",
	KnownPerformers: map[string]bool{
		"lola noir":        true,
		"mr l8x":           true,
		"miss fetilicious": true,
		"latex lara":       true,
		"lizanne":          true,
		"latex lea":        true,
		"rubber jeff":      true,
	},
})

// Scraper implements scraper.StudioScraper for Lola Noir.
type Scraper struct {
	mm *mymemberutil.Scraper
}

// New constructs a Lola Noir scraper.
func New() *Scraper { return &Scraper{mm: mm} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string         { return mm.Config().SiteID }
func (s *Scraper) Patterns() []string { return []string{"latexlolanoir.com"} }

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?latexlolanoir\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.mm.Run(ctx, studioURL, opts, out)
	}()
	return out, nil
}
