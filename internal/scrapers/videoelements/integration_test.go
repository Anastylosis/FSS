//go:build integration

package videoelements

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/internal/scrapers/veutil"
)

func newTestScraper(cfg siteConfig) *veutil.Scraper {
	escaped := strings.ReplaceAll(cfg.Domain, ".", `\.`)
	re := regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?%s(/|$)`, escaped))

	return veutil.New(veutil.SiteConfig{
		ID:             cfg.SiteID,
		Studio:         cfg.StudioName,
		SiteBase:       "https://" + cfg.Domain,
		MainCategoryID: cfg.MainCategoryID,
		Patterns:       []string{cfg.Domain},
		MatchRe:        re,
	})
}

func TestLiveMommysBoy(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(sites[5]), "https://mommysboy.net/", 2)
}

func TestLiveMommysBoyTagPage(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(sites[5]), "https://mommysboy.net/tag/cory-chase/", 2)
}

func TestLiveAbuseMe(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(sites[13]), "https://abuseme.net/", 3)
}
