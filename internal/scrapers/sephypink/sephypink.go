// Package sephypink scrapes Persephone Pink (sephypink.com), a MyMember.site
// instance on its own domain. Listing and detail handling live in
// mymemberutil; this package only supplies the site config. persephonepink.com
// is a link landing page with no catalogue and is not matched.
package sephypink

import (
	"context"
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/mymemberutil"
	"github.com/Anastylosis/FSS/scraper"
)

var mm = mymemberutil.New(mymemberutil.SiteConfig{
	SiteID:     "sephypink",
	Domain:     "sephypink.com",
	StudioName: "Persephone Pink",
})

// Scraper implements scraper.StudioScraper for Persephone Pink.
type Scraper struct {
	mm *mymemberutil.Scraper
}

// New constructs a Persephone Pink scraper.
func New() *Scraper { return &Scraper{mm: mm} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string         { return mm.Config().SiteID }
func (s *Scraper) Patterns() []string { return []string{"sephypink.com"} }

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?sephypink\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.mm.Run(ctx, studioURL, opts, out)
	}()
	return out, nil
}
