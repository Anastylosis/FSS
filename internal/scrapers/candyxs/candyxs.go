// Package candyxs scrapes CandyXS, a MyMember.site instance served under the
// platform's own domain rather than its own. All listing and detail handling
// lives in mymemberutil; this package only supplies the site config.
package candyxs

import (
	"context"
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/mymemberutil"
	"github.com/Anastylosis/FSS/scraper"
)

var mm = mymemberutil.New(mymemberutil.SiteConfig{
	SiteID:     "candyxs",
	Domain:     "mymember.site",
	BasePath:   "/candyxs",
	StudioName: "CandyXS",
})

// Scraper implements scraper.StudioScraper for CandyXS.
type Scraper struct {
	mm *mymemberutil.Scraper
}

// New constructs a CandyXS scraper.
func New() *Scraper { return &Scraper{mm: mm} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return mm.Config().SiteID }

func (s *Scraper) Patterns() []string {
	return []string{"mymember.site/candyxs", "candyxs.com"}
}

// matchRe accepts both the platform path and the vanity domain that redirects
// to it, so either spelling reaches this scraper.
var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:mymember\.site/candyxs|candyxs\.com)(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.mm.Run(ctx, studioURL, opts, out)
	}()
	return out, nil
}
