//go:build integration

package empirestore

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/empirestoreutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func newSiteScraper(cfg siteConfig) *siteScraper {
	escaped := strings.ReplaceAll(cfg.Domain, ".", `\.`)
	return &siteScraper{
		es: empirestoreutil.New(empirestoreutil.SiteConfig{
			SiteID:     cfg.SiteID,
			Domain:     cfg.Domain,
			StudioName: cfg.StudioName,
			ListingURL: cfg.ListingURL,
		}),
		config: cfg,
		re:     regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?%s(?:/|$)`, escaped)),
	}
}

func TestLiveElegantAngel(t *testing.T) {
	const u = "https://www.elegantangel.com/watch-newest-elegant-angel-clips-and-scenes.html"
	testutil.SkipIfPlaceholder(t, u)
	cfg := siteConfig{"elegantangel", "elegantangel.com", "Elegant Angel", "/watch-newest-elegant-angel-clips-and-scenes.html"}
	testutil.RunLiveScrape(t, newSiteScraper(cfg), u, 2)
}

func TestLiveVouyerMedia(t *testing.T) {
	const u = "https://vouyermedia.com/watch-newest-vouyer-media-clips-and-scenes.html"
	testutil.SkipIfPlaceholder(t, u)
	cfg := siteConfig{"vouyermedia", "vouyermedia.com", "Vouyer Media", "/watch-newest-vouyer-media-clips-and-scenes.html"}
	testutil.RunLiveScrape(t, newSiteScraper(cfg), u, 2)
}

func TestLiveReaganFoxx(t *testing.T) {
	const u = "https://www.reaganfoxx.com/scenes/673608/reagan-foxx-streaming-pornstar-videos.html"
	testutil.SkipIfPlaceholder(t, u)
	cfg := siteConfig{"reaganfoxx", "reaganfoxx.com", "Reagan Foxx", "/scenes/673608/reagan-foxx-streaming-pornstar-videos.html"}
	testutil.RunLiveScrape(t, newSiteScraper(cfg), u, 2)
}

func TestLiveBobsVideos(t *testing.T) {
	const u = "https://bobsvideos.empirestores.co/shop-streaming-video-by-scene.html"
	testutil.SkipIfPlaceholder(t, u)
	cfg := siteConfig{"bobsvideos", "bobsvideos.empirestores.co", "Bob's Videos", "/shop-streaming-video-by-scene.html"}
	testutil.RunLiveScrape(t, newSiteScraper(cfg), u, 2)
}

func TestLiveJodiWest(t *testing.T) {
	const u = "https://jodiwest.com/watch-newest-jodi-west-clips-and-scenes.html"
	testutil.SkipIfPlaceholder(t, u)
	cfg := siteConfig{"jodiwest", "jodiwest.com", "Jodi West", "/watch-newest-jodi-west-clips-and-scenes.html"}
	testutil.RunLiveScrape(t, newSiteScraper(cfg), u, 2)
}

// A `/{id}/studio/…` URL is a shop page that redirects to the store's front
// page, so it is rewritten to the store's scene listing filtered by that
// studio id.
func TestLiveStudioFilter(t *testing.T) {
	const u = "https://bobsvideos.empirestores.co/427/studio/bobs-videos-streaming-porn-movies.html"
	testutil.SkipIfPlaceholder(t, u)
	cfg := siteConfig{"bobsvideos", "bobsvideos.empirestores.co", "Bob's Videos", "/shop-streaming-video-by-scene.html"}
	testutil.RunLiveScrape(t, newSiteScraper(cfg), u, 1)
}
