// Package stmackenzies scrapes St Mackenzie's (stmackenzies.com), a MyMember.site
// instance on its own domain. All listing and detail handling lives in
// mymemberutil; this package only supplies the site config.
package stmackenzies

import (
	"context"
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/mymemberutil"
	"github.com/Anastylosis/FSS/scraper"
)

var mm = mymemberutil.New(mymemberutil.SiteConfig{
	SiteID:     "stmackenzies",
	Domain:     "stmackenzies.com",
	StudioName: "St Mackenzie's",
})

// Scraper implements scraper.StudioScraper for St Mackenzie's.
type Scraper struct {
	mm *mymemberutil.Scraper
}

// New constructs a St Mackenzie's scraper.
func New() *Scraper { return &Scraper{mm: mm} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string         { return mm.Config().SiteID }
func (s *Scraper) Patterns() []string { return []string{"stmackenzies.com"} }

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?stmackenzies\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.mm.Run(ctx, studioURL, opts, out)
	}()
	return out, nil
}
