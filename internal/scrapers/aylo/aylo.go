package aylo

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Anastylosis/FSS/internal/scrapers/ayloutil"
	"github.com/Anastylosis/FSS/scraper"
)

type siteConfig struct {
	SiteID        string
	Domain        string
	StudioName    string
	ExtraPatterns []string // additional patterns before the common ones
	AltDomains    []string // additional domains that resolve to the same site
	ScenePath     string   // URL path segment for scenes (default: "video")
}

var sites = []siteConfig{
	{"babes", "babes.com", "Babes", []string{"babes.com/model/{id}/{slug}"}, nil, ""},
	{"bigstr", "czechhunter.com", "BigStr", nil, []string{"bigstr.com", "debtdandy.com", "dirtyscout.com"}, ""},
	{"brazzers", "brazzers.com", "Brazzers", nil, nil, ""},
	{"bromo", "bromo.com", "Bromo", nil, nil, "scene"},
	{"digitalplayground", "digitalplayground.com", "Digital Playground", []string{"digitalplayground.com/modelprofile/{id}/{slug}"}, []string{"digitalplaygroundnetwork.com"}, ""},
	{"erito", "erito.com", "Erito", nil, nil, ""},
	{"fakehub", "fakehub.com", "FakeHub", nil, []string{"publicagent.com", "faketaxi.com", "fakehostel.com", "fakedrivingschool.com", "femalefaketaxi.com", "fakeagent.com", "fakehospital.com", "femaleagent.com", "fakecop.com", "fakeagentuk.com", "fakehuboriginals.com"}, "scene"},
	{"hentaipros", "hentaipros.com", "HentaiPros", nil, nil, ""},
	{"killergram", "killergram.com", "Killergram", nil, nil, ""},
	{"letsdoeit", "letsdoeit.com", "LetsDoeIt", nil, nil, ""},
	{"men", "men.com", "Men", nil, []string{"bigdicksatschool.com", "drillmyhole.com", "str8togay.com", "thegayoffice.com", "jizzorgy.com", "menofuk.com", "toptobottom.com"}, "sceneid"},
	{"metro", "shewillcheat.com", "Metro", nil, []string{"metrohd.com", "familyhookups.com", "kinkyspa.com", "girlgrind.com", "devianthardcore.com"}, ""},
	{"milehigh", "milfed.com", "MileHigh", nil, []string{"dilfed.com", "gilfed.com"}, "scene"},
	{"mofos", "mofos.com", "Mofos", []string{"mofos.com/model/{id}/{slug}"}, nil, ""},
	{"propertysex", "propertysex.com", "PropertySex", nil, nil, ""},
	{"realitydudes", "realitydudes.com", "RealityDudes", nil, []string{"boysfirsttime.com", "dickdorm.com", "gayrevenge.com", "papi.com", "str8chaser.com"}, ""},
	{"realitykings", "realitykings.com", "Reality Kings", []string{"realitykings.com/model/{id}/{slug}"}, []string{"rk.com"}, ""},
	{"seancody", "seancody.com", "Sean Cody", nil, nil, ""},
	{"squirted", "squirted.com", "Squirted", nil, nil, ""},
	{"transangels", "transangels.com", "TransAngels", nil, nil, ""},
	{"twinkpop", "twinkpop.com", "TwinkPop", nil, nil, ""},
	{"twistys", "twistys.com", "Twistys", nil, nil, ""},
	{"whynotbi", "whynotbi.com", "WhyNotBi", nil, nil, "scene"},

	// SpiceVids-brand standalone domains
	{"househumpers", "househumpers.com", "House Humpers", nil, nil, "scene"},
	{"nextdoorhobby", "nextdoorhobby.com", "NextDoorHobby", nil, nil, "scene"},
	{"trueamateurs", "trueamateurs.com", "True Amateurs", nil, nil, "scene"},

	// Mile High Media sub-sites
	{"biempire", "biempire.com", "BiEmpire", nil, nil, "scene"},
	{"doghousedigital", "doghousedigital.com", "Doghouse Digital", nil, nil, "scene"},
	{"familysinners", "familysinners.com", "Family Sinners", nil, nil, "scene"},
	{"iconmale", "iconmale.com", "Icon Male", nil, nil, "scene"},
	{"noirmale", "noirmale.com", "Noir Male", nil, nil, "scene"},
	{"realityjunkies", "realityjunkies.com", "Reality Junkies", nil, nil, "scene"},
	{"sweetsinner", "sweetsinner.com", "Sweet Sinner", nil, nil, "scene"},
	{"sweetheartvideo", "sweetheartvideo.com", "Sweetheart Video", nil, nil, "scene"},
	{"transsensual", "transsensual.com", "Transsensual", nil, nil, "scene"},

	// BangBros sub-sites with standalone domains (content also on bangbros.com)
	{"dancingbear", "dancingbear.com", "Dancing Bear", nil, nil, "scene"},
	{"sexselector", "sexselector.com", "Sex Selector", nil, nil, "scene"},
	{"virtualporn", "virtualporn.com", "Virtual Porn", nil, nil, ""},

	// Sexy Hub network (Aylo). Sites with their own working Aylo instance get a
	// per-brand entry for proper attribution; the sexyhub.com hub covers the
	// whole network (incl. Mom XXX, Girlfriends, Kissing HD, Orgasms.XXX,
	// BDSM.xxx — brands whose standalone domains no longer expose their own
	// instance token). (Mom XXX's own domain collides with the existing
	// videoelements "momxxx" scraper, so it is covered via the hub instead.)
	{"danejones", "danejones.com", "Dane Jones", nil, nil, ""},
	{"lesbea", "lesbea.com", "Lesbea", nil, nil, ""},
	{"fitnessrooms", "fitnessrooms.com", "Fitness Rooms", nil, nil, ""},
	{"massagerooms", "massagerooms.com", "Massage Rooms", nil, nil, ""},
	{"sexyhub", "sexyhub.com", "Sexy Hub", nil, nil, ""},

	// Gay Wire network (Aylo). Every brand with a live standalone domain gets
	// its own entry so scenes are attributed to the brand rather than the hub;
	// the gaywire.com entry covers the brands that only exist as
	// gaywire.com/websites/{slug} pages (Bareback Attack, Bareback Casting,
	// ExBF, Pound His Ass, Rub Him, Urban Invasion). projectcitybus.com no
	// longer resolves and has no entry.
	{"gaywire", "gaywire.com", "Gay Wire", nil, nil, "scene"},
	{"baitbus", "baitbus.com", "Bait Bus", nil, nil, "scene"},
	{"outinpublic", "outinpublic.com", "Out In Public", nil, nil, "scene"},
	{"hazehim", "hazehim.com", "Haze Him", nil, nil, "scene"},
	{"itsgonnahurt", "itsgonnahurt.com", "It's Gonna Hurt", nil, nil, "scene"},
	{"thughunter", "thughunter.com", "Thug Hunter", nil, nil, "scene"},
	{"ungloryhole", "ungloryhole.com", "UngloryHole", nil, nil, "scene"},
	{"gaypatrol", "gaypatrol.com", "Gay Patrol", nil, nil, "scene"},
	{"gaypawn", "gaypawn.com", "Gay Pawn", nil, nil, "scene"},
	{"grabass", "grabass.com", "Grab Ass", nil, nil, "scene"},
	{"guyselector", "guyselector.com", "Guy Selector", nil, nil, "scene"},
	{"sausageparty", "sausageparty.com", "Sausage Party", nil, nil, "scene"},
	{"bigdaddy", "bigdaddy.com", "Bigdaddy", nil, nil, "scene"},
}

type siteScraper struct {
	aylo     *ayloutil.Scraper
	config   siteConfig
	matchRe  *regexp.Regexp
	patterns []string
}

var _ scraper.StudioScraper = (*siteScraper)(nil)

func (s *siteScraper) ID() string               { return s.config.SiteID }
func (s *siteScraper) Patterns() []string       { return s.patterns }
func (s *siteScraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *siteScraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.aylo.Run(ctx, studioURL, opts, out)
	return out, nil
}

func init() {
	for _, cfg := range sites {
		allDomains := append([]string{cfg.Domain}, cfg.AltDomains...)
		var reparts []string
		for _, d := range allDomains {
			reparts = append(reparts, strings.ReplaceAll(d, ".", `\.`))
		}
		re := regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?(?:%s)(?:/|$)`, strings.Join(reparts, "|")))

		patterns := []string{cfg.Domain}
		patterns = append(patterns, cfg.AltDomains...)
		patterns = append(patterns, cfg.ExtraPatterns...)
		patterns = append(patterns,
			cfg.Domain+"/pornstar/{id}/{slug}",
			cfg.Domain+"/category/{id}/{slug}",
			cfg.Domain+"/site/{id}/{slug}",
			cfg.Domain+"/series/{id}/{slug}",
		)

		ayloCfg := ayloutil.SiteConfig{
			SiteID:     cfg.SiteID,
			SiteBase:   "https://www." + cfg.Domain,
			StudioName: cfg.StudioName,
			ScenePath:  cfg.ScenePath,
		}

		s := &siteScraper{
			aylo:     ayloutil.New(ayloCfg),
			config:   cfg,
			matchRe:  re,
			patterns: patterns,
		}
		scraper.Register(s)
	}
}
