// Package riggsfilmsvip scrapes riggsfilms.vip, the Riggs Films members store
// on MyMember.site. All listing and detail handling lives in mymemberutil; this
// package only supplies the site config.
package riggsfilmsvip

import (
	"context"
	"regexp"
	"strings"

	"github.com/Anastylosis/FSS/internal/scrapers/mymemberutil"
	"github.com/Anastylosis/FSS/scraper"
)

var mm = mymemberutil.New(mymemberutil.SiteConfig{
	SiteID:          "riggsfilmsvip",
	Domain:          "riggsfilms.vip",
	StudioName:      "Riggs Films",
	TitlePerformers: true,
	DropDescription: genericDescription,
	KnownPerformers: map[string]bool{
		"alycia heart":      true,
		"bella maree":       true,
		"boswell black":     true,
		"brad banks":        true,
		"brock cooper":      true,
		"chad white":        true,
		"chloe amour":       true,
		"christine mcqueen": true,
		"coco bae":          true,
		"danae mari":        true,
		"ember snow":        true,
		"fit kitty":         true,
		"gabbie rose":       true,
		"holli curtis":      true,
		"hope ryan":         true,
		"isabelle deltore":  true,
		"jack wanton":       true,
		"jordan millicent":  true,
		"katija cortez":     true,
		"kayla jade":        true,
		"kayla lea":         true,
		"kitana montana":    true,
		"luci power":        true,
		"muscle barbie":     true,
		"olive may":         true,
		"ryan james":        true,
		"sarah jessie":      true,
		"savanah storm":     true,
		"sharni jane":       true,
		"stefani marz":      true,
		"tayla xoxo":        true,
		"tiffany watson":    true,
		"zac wild":          true,
	},
})

// genericDescription spots the storefront blurb (or a pasted link) that scenes
// without their own synopsis fall back to.
func genericDescription(d string) bool {
	return strings.HasPrefix(d, "Riggs Films. Australia") || strings.HasPrefix(d, "http")
}

// Scraper implements scraper.StudioScraper for riggsfilms.vip.
type Scraper struct {
	mm *mymemberutil.Scraper
}

// New constructs a Riggs Films (VIP) scraper.
func New() *Scraper { return &Scraper{mm: mm} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string         { return mm.Config().SiteID }
func (s *Scraper) Patterns() []string { return []string{"riggsfilms.vip"} }

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?riggsfilms\.vip(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.mm.Run(ctx, studioURL, opts, out)
	}()
	return out, nil
}
