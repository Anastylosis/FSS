//go:build integration

package uptimely

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/internal/scrapers/uptimelyutil"
)

func findSite(id string) siteConfig {
	for _, s := range sites {
		if s.SiteID == id {
			return s
		}
	}
	panic("site not found: " + id)
}

func newTestScraper(cfg siteConfig) *uptimelyutil.Scraper {
	return uptimelyutil.New(uptimelyutil.SiteConfig{
		ID:     cfg.SiteID,
		Studio: cfg.StudioName,
		Domain: cfg.Domain,
		Patterns: []string{
			cfg.Domain + "/works/list/series/{id}",
			cfg.Domain + "/works/list/release",
			cfg.Domain + "/works/list/date/{date}",
			cfg.Domain + "/works/list/genre/{id}",
			cfg.Domain + "/works/list/label/{id}",
			cfg.Domain + "/actress/detail/{id}",
		},
		MatchRe: matchRe(cfg.Domain),
	})
}

func TestLiveHHHGroup(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("hhhgroup")), "https://hhh-av.com/works/list/release", 2)
}

func TestLiveIdeaPocket(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("ideapocket")), "https://ideapocket.com/works/list/release", 2)
}

func TestLiveHonnaka(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("honnaka")), "https://honnaka.jp/works/list/genre/104", 2)
}

func TestLiveKawaii(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("kawaii")), "https://kawaiikawaii.jp/works/list/genre/104", 2)
}

func TestLiveWanzFactory(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("wanzfactory")), "https://wanz-factory.com/works/list/release", 2)
}

func TestLiveOppai(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("oppai")), "https://oppai-av.com/works/list/release", 2)
}

func TestLiveEbody(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("ebody")), "https://av-e-body.com/works/list/release", 2)
}

func TestLiveChijoHeaven(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("chijoheaven")), "https://bi-av.com/works/list/release", 2)
}

func TestLiveTameikeGoro(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("tameikegoro")), "https://tameikegoro.jp/works/list/genre/113", 2)
}

// The bare host is the catalogue mode: the genre index is walked instead of
// the release page, which shows only the newest works.
func TestLiveTameikeGoroCatalogue(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("tameikegoro")), "https://tameikegoro.jp/", 2)
}

func TestLiveKiraKira(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("kirakira")), "https://kirakira-av.com/works/list/release", 2)
}
