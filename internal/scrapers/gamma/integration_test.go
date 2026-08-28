//go:build integration

package gamma

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/gammautil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func newTestScraper(cfg siteConfig) *siteScraper {
	var re *regexp.Regexp
	if cfg.MatchRe != "" {
		re = regexp.MustCompile(cfg.MatchRe)
	} else {
		escaped := strings.ReplaceAll(cfg.Domain, ".", `\.`)
		re = regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?%s`, escaped))
	}

	siteName := cfg.SiteName
	if siteName == "" && cfg.StudioName != "" {
		siteName = cfg.SiteID
	}

	gammaCfg := gammautil.SiteConfig{
		SiteID:          cfg.SiteID,
		SiteBase:        "https://www." + cfg.Domain,
		StudioName:      cfg.StudioName,
		SiteName:        siteName,
		RefererBase:     cfg.RefererBase,
		BootstrapPage:   cfg.BootstrapPage,
		ScenePathPrefix: cfg.ScenePathPrefix,
	}

	return &siteScraper{
		gamma:   gammautil.New(gammaCfg),
		config:  cfg,
		matchRe: re,
	}
}

func findSite(id string) siteConfig {
	for _, cfg := range sites {
		if cfg.SiteID == id {
			return cfg
		}
	}
	panic("site not found: " + id)
}

func TestLiveBurningAngel(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("burningangel")), "https://www.burningangel.com/", 2)
}

func TestLivePureTaboo(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("puretaboo")), "https://www.puretaboo.com/", 2)
}

// Vivid network — every sub-site uses the same gamma/Algolia pipeline
// with RefererBase pinned to vivid.com (the API key is signed for the
// parent's Referer, so sub-domain Referers get HTTP 403 from Algolia).
// One live smoke per site validates that wiring across all 13 entries.
func TestLiveVivid(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("vivid")), "https://www.vivid.com/", 2)
}

func TestLive65InchHugeAsses(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("65inchhugeasses")), "https://www.65inchhugeasses.com/", 2)
}

func TestLiveBlackWhiteFuckfest(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("blackwhitefuckfest")), "https://www.blackwhitefuckfest.com/", 2)
}

func TestLiveBrandNewFaces(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("brandnewfaces")), "https://www.brandnewfaces.com/", 2)
}

func TestLiveGirlsWhoFuckGirls(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("girlswhofuckgirls")), "https://www.girlswhofuckgirls.com/", 2)
}

func TestLiveMomIsAMilf(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("momisamilf")), "https://www.momisamilf.com/", 2)
}

func TestLiveNastyStepFamily(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("nastystepfamily")), "https://www.nastystepfamily.com/", 2)
}

func TestLiveNineteen(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("nineteen")), "https://www.nineteen.com/", 2)
}

func TestLiveOrgyTrain(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("orgytrain")), "https://www.orgytrain.com/", 2)
}

func TestLivePetited(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("petited")), "https://www.petited.com/", 2)
}

func TestLiveTheBrats(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("thebrats")), "https://www.thebrats.com/", 2)
}

func TestLiveVividClassic(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("vividclassic")), "https://www.vividclassic.com/", 2)
}

func TestLiveWhereTheBoysArent(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("wheretheboysarent")), "https://www.wheretheboysarent.com/", 2)
}

func TestLivePlayboyTV(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("playboytv")), "https://www.playboytv.com/", 2)
}

// Alpha Studio Group — own-domain gay studios, each bootstrapping its own
// Algolia segment key. One smoke per site validates the segment wiring.
func TestLiveChaosMen(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("chaosmen")), "https://www.chaosmen.com/", 2)
}

func TestLiveActiveDuty(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("activeduty")), "https://www.activeduty.com/", 2)
}

func TestLiveDisruptiveFilms(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("disruptivefilms")), "https://www.disruptivefilms.com/", 2)
}

func TestLiveSodomySquad(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("sodomysquad")), "https://www.sodomysquad.com/", 2)
}

func TestLiveASGmax(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("asgmax")), "https://www.asgmax.com/", 2)
}

// XEmpire — shared segment:xempire key, per-brand availableOnSite filter.
func TestLiveHardX(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("hardx")), "https://www.hardx.com/", 2)
}

func TestLiveDarkX(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("darkx")), "https://www.darkx.com/", 2)
}

func TestLiveEroticaX(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("eroticax")), "https://www.eroticax.com/", 2)
}

func TestLiveAllBlackX(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("allblackx")), "https://www.allblackx.com/", 2)
}

func TestLiveLesbianX(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("lesbianx")), "https://www.lesbianx.com/", 2)
}

func TestLiveXEmpire(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("xempire")), "https://www.xempire.com/", 2)
}

// Falcon | NakedSword — shared segment:falconstudios key.
func TestLiveFalconStudios(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("falconstudios")), "https://www.falconstudios.com/", 2)
}

func TestLiveHotHouse(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("hothouse")), "https://www.hothouse.com/", 2)
}

func TestLiveRagingStallion(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("ragingstallion")), "https://www.ragingstallion.com/", 2)
}

// 21 Sextury — shared segment:21sextury key, per-brand availableOnSite filter.
func TestLive21Sextury(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("21sextury")), "https://www.21sextury.com/", 2)
}

func TestLiveAssholeFever(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("assholefever")), "https://www.assholefever.com/", 2)
}

func TestLiveDPFanatics(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("dpfanatics")), "https://www.dpfanatics.com/", 2)
}

func TestLiveFootsieBabes(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("footsiebabes")), "https://www.footsiebabes.com/", 2)
}

func TestLiveLezCuties(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("lezcuties")), "https://www.lezcuties.com/", 2)
}

func TestLiveAnalTeenAngels(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("analteenangels")), "https://www.analteenangels.com/", 2)
}

func TestLiveNudeFightClub(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("nudefightclub")), "https://www.nudefightclub.com/", 2)
}

func TestLiveNuruMassage(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("nurumassage")), "https://www.nurumassage.com/", 2)
}

func TestLiveAllGirlMassage(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("allgirlmassage")), "https://www.allgirlmassage.com/", 2)
}

func TestLiveLustyGrandmas(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("lustygrandmas")), "https://lustygrandmas.21sextreme.com/", 2)
}

func TestLive21Naturals(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("21naturals")), "https://21naturals.com/", 2)
}

func TestLive21FootArt(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("21footart")), "https://21footart.21naturals.com/en", 2)
}

func TestLiveLethalHardcoreVR(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("lethalhardcorevr")), "https://www.lethalhardcorevr.com/", 2)
}

func TestLiveGenderXFilms(t *testing.T) {
	testutil.RunLiveScrape(t, newTestScraper(findSite("genderxfilms")), "https://www.genderxfilms.com/", 2)
}

// Pride Studios — like the Vivid network, some brands redirect to the hub and
// their Algolia key is Referer-signed for pridestudios.com, so RefererBase has
// to be pinned or Algolia answers HTTP 403. One live smoke per site validates
// that wiring across all 10 entries.
func TestLivePrideStudios(t *testing.T) {
	cases := []struct{ id, url string }{
		// Own-domain sites.
		{"extrabigdicks", "https://www.extrabigdicks.com/en/videos"},
		{"menover30", "https://www.menover30.com/en/videos"},
		{"familycreep", "https://www.familycreep.com/en/videos"},
		// Sites that redirect to the hub (RefererBase pinned).
		{"circlejerkboys", "https://www.circlejerkboys.com/en/videos"},
		{"boyzparty", "https://www.boyzparty.com/en/videos"},
		{"highperformancemen", "https://www.highperformancemen.com/en/videos"},
		{"dylanlucas", "https://www.dylanlucas.com/en/videos"},
		{"cockvirgins", "https://www.cockvirgins.com/en/videos"},
		{"bearback", "https://www.bearback.com/en/videos"},
		// Hub.
		{"pridestudios", "https://www.pridestudios.com/en/videos"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			testutil.RunLiveScrape(t, newTestScraper(findSite(c.id)), c.url, 2)
		})
	}
}

func TestLiveFantasyMassage(t *testing.T) {
	cfg := findSite("fantasymassage")
	testutil.RunLiveScrape(t, newTestScraper(cfg), "https://www."+cfg.Domain+"/", 2)
}
