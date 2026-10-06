package zlatexa

import (
	"context"
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/mymemberutil"
	"github.com/Anastylosis/FSS/scraper"
)

var mm = mymemberutil.New(mymemberutil.SiteConfig{
	SiteID:     "zlatexa",
	Domain:     "zlatexa.com",
	StudioName: "Zlatexa",
	KnownPerformers: map[string]bool{
		"zlatexa_": true,
	},
})

type Scraper struct {
	mm *mymemberutil.Scraper
}

func New() *Scraper {
	return &Scraper{mm: mm}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string         { return mm.Config().SiteID }
func (s *Scraper) Patterns() []string { return []string{"zlatexa.com"} }

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?zlatexa\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.mm.Run(ctx, studioURL, opts, out)
	}()
	return out, nil
}
