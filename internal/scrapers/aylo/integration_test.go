//go:build integration

package aylo

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/ayloutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func newTestScraper(cfg siteConfig) *siteScraper {
	allDomains := append([]string{cfg.Domain}, cfg.AltDomains...)
	var reparts []string
	for _, d := range allDomains {
		reparts = append(reparts, strings.ReplaceAll(d, ".", `\.`))
	}
	re := regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?(?:%s)`, strings.Join(reparts, "|")))

	ayloCfg := ayloutil.SiteConfig{
		SiteID:     cfg.SiteID,
		SiteBase:   "https://www." + cfg.Domain,
		StudioName: cfg.StudioName,
		ScenePath:  cfg.ScenePath,
	}

	return &siteScraper{
		aylo:    ayloutil.New(ayloCfg),
		config:  cfg,
		matchRe: re,
	}
}

func TestLiveBabes(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(sites[0]), "https://www.babes.com/", 2)
}

func TestLiveBrazzers(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(sites[2]), "https://www.brazzers.com/", 2)
}

func TestLiveRealityKings(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(sites[16]), "https://www.realitykings.com/", 2)
}

func TestLiveSexyHub(t *testing.T) {
	for _, id := range []string{"danejones", "lesbea", "fitnessrooms", "massagerooms", "sexyhub"} {
		id := id
		t.Run(id, func(t *testing.T) {
			var cfg siteConfig
			for _, c := range sites {
				if c.SiteID == id {
					cfg = c
					break
				}
			}
			testutil.RunLiveScrape(t, newTestScraper(cfg), "https://www."+cfg.Domain+"/", 2)
		})
	}
}

func TestLiveGayWire(t *testing.T) {
	for _, id := range []string{
		"gaywire", "baitbus", "outinpublic", "hazehim", "itsgonnahurt", "thughunter",
		"ungloryhole", "gaypatrol", "gaypawn", "grabass", "guyselector", "sausageparty", "bigdaddy",
	} {
		id := id
		t.Run(id, func(t *testing.T) {
			var cfg siteConfig
			for _, c := range sites {
				if c.SiteID == id {
					cfg = c
					break
				}
			}
			testutil.RunLiveScrape(t, newTestScraper(cfg), "https://www."+cfg.Domain+"/", 2)
		})
	}
}
