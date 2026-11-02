//go:build integration

package czechav

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/czechavutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveCzechCasting(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "czechcasting", Domain: "czechcasting.com", Studio: "Czech Casting"})
	testutil.RunLiveScrape(t, s, "https://czechcasting.com", 3)
}

func TestLiveHorrorPorn(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "horrorporn", Domain: "horrorporn.com", Studio: "Horror Porn"})
	testutil.RunLiveScrape(t, s, "https://horrorporn.com", 3)
}

func TestLiveCzechStreets(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "czechstreets", Domain: "czechstreets.com", Studio: "Czech Streets"})
	testutil.RunLiveScrape(t, s, "https://czechstreets.com", 3)
}

func TestLiveCzechCastingPerformerSearch(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "czechcasting", Domain: "czechcasting.com", Studio: "Czech Casting"})
	testutil.RunLiveScrape(t, s, "https://czechcasting.com/pages/search/?q=Tereza&adult-performer", 2)
}

func TestLiveCzechGayAmateurs(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "czechgayamateurs", Domain: "czechgayamateurs.com", Studio: "Czech Gay Amateurs"})
	testutil.RunLiveScrape(t, s, "https://czechgayamateurs.com", 3)
}

func TestLiveCzechGayCasting(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "czechgaycasting", Domain: "czechgaycasting.com", Studio: "Czech Gay Casting"})
	testutil.RunLiveScrape(t, s, "https://czechgaycasting.com", 3)
}

func TestLiveCzechGayCouples(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "czechgaycouples", Domain: "czechgaycouples.com", Studio: "Czech Gay Couples"})
	testutil.RunLiveScrape(t, s, "https://czechgaycouples.com", 3)
}

func TestLiveCzechGayFantasy(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "czechgayfantasy", Domain: "czechgayfantasy.com", Studio: "Czech Gay Fantasy"})
	testutil.RunLiveScrape(t, s, "https://czechgayfantasy.com", 3)
}

func TestLiveCzechGayMassage(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "czechgaymassage", Domain: "czechgaymassage.com", Studio: "Czech Gay Massage"})
	testutil.RunLiveScrape(t, s, "https://czechgaymassage.com", 3)
}

func TestLiveCzechGaySolarium(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "czechgaysolarium", Domain: "czechgaysolarium.com", Studio: "Czech Gay Solarium"})
	testutil.RunLiveScrape(t, s, "https://czechgaysolarium.com", 3)
}

func TestLiveGayHorrorPorn(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "gayhorrorporn", Domain: "gayhorrorporn.com", Studio: "Gay Horror Porn"})
	testutil.RunLiveScrape(t, s, "https://gayhorrorporn.com", 3)
}

func TestLiveGlaminoGirls(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "glaminogirls", Domain: "glaminogirls.com", Studio: "Glamino Girls"})
	testutil.RunLiveScrape(t, s, "https://glaminogirls.com", 3)
}

func TestLiveFuckMeSensei(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "fuckmesensei", Domain: "fuckmesensei.com", Studio: "Fuck Me Sensei"})
	testutil.RunLiveScrape(t, s, "https://fuckmesensei.com", 3)
}

func TestLiveGhostPorn(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "ghostporn", Domain: "ghost-porn.com", Studio: "Ghost Porn"})
	testutil.RunLiveScrape(t, s, "https://ghost-porn.com", 3)
}

func TestLiveLoveASMR(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "loveasmr", Domain: "loveasmr.com", Studio: "Love ASMR"})
	testutil.RunLiveScrape(t, s, "https://loveasmr.com", 3)
}

func TestLiveSpy26(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "spy26", Domain: "spy26.com", Studio: "Spy 26"})
	testutil.RunLiveScrape(t, s, "https://spy26.com", 3)
}

func TestLiveTherapyASMR(t *testing.T) {
	s := czechavutil.New(czechavutil.SiteConfig{SiteID: "therapyasmr", Domain: "therapyasmr.com", Studio: "Therapy ASMR"})
	testutil.RunLiveScrape(t, s, "https://therapyasmr.com", 3)
}
