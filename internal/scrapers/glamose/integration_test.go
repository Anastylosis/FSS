//go:build integration

package glamose

import (
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/internal/scrapers/utgutil"
)

func site(t *testing.T, id string) *utgutil.Scraper {
	t.Helper()
	for _, cfg := range sites {
		if cfg.SiteID == id {
			return utgutil.New(cfg)
		}
	}
	t.Fatalf("no site %q", id)
	return nil
}

func TestLiveHayleysSecrets(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "hayleyssecrets"), "https://hayleyssecrets.com/updates/videos", 2)
}

func TestLiveHayleysSecretsModel(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "hayleyssecrets"), "https://hayleyssecrets.com/models/hayley-marie-coppin", 2)
}

func TestLiveMoreThanNylons(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "morethannylons"), "https://www.morethannylons.com/updates/videos", 2)
}

func TestLiveSkinTightGlamour(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "skintightglamour"), "https://www.skintightglamour.com/updates/videos", 2)
}

func TestLiveUKTickling(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "uktickling"), "https://www.uktickling.com/updates/videos", 2)
}

func TestLiveWorshipJasmine(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "worshipjasmine"), "https://www.worshipjasmine.com/updates/videos", 2)
}

func TestLiveAllBrookWright(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "allbrookwright"), "https://www.allbrookwright.com/updates/videos", 2)
}

func TestLiveBethMorgan(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "bethmorganofficial"), "https://www.bethmorganofficial.com/updates/videos", 2)
}

func TestLiveSophia(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "sophiassexylegwear"), "https://www.sophiassexylegwear.com/updates/videos", 2)
}

func TestLiveBreathTakers(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "breathtakers"), "https://www.breath-takers.com/updates/videos", 2)
}

func TestLiveGirlfolio(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "girlfolio"), "https://www.girlfolio.com/updates/videos", 2)
}

func TestLiveGlamosePortal(t *testing.T) {
	testutil.RunLiveScrape(t, &portalScraper{client: httpx.NewClient(30 * time.Second)}, "https://www.glamose.com/", 2)
}
