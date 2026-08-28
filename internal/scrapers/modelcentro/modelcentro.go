package modelcentro

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Anastylosis/FSS/internal/scrapers/modelcentroutil"
	"github.com/Anastylosis/FSS/scraper"
)

type siteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	Performers []string // solo-performer sites
	// Aliases are extra domains that redirect to Domain. Requests still go to
	// Domain; these only widen URL matching so a redirecting domain a user
	// pastes still resolves to this scraper.
	Aliases []string
}

var sites = []siteConfig{
	{"backalleytoonz", "backalleytoonzonline.com", "Backalley Toonz", nil, nil},
	{"bigjohnnyxxx", "bigjohnnyxxx.com", "Big Johnny XXX", nil, nil},
	{"blackmoneyerotica", "blackmoneyerotica.com", "Black Money Erotica", nil, nil},
	{"blackpynk", "blackpynk.com", "Black Pynk", nil, nil},
	{"brookelynnebriar", "brookelynnebriar.com", "Brookelynne Briar", []string{"Brookelynne Briar"}, nil},
	{"citygirlz", "sallydangeloxxx.com", "City Girlz", nil, nil},
	{"cumtrainer", "cumtrainer.com", "Cum Trainer", nil, nil},
	{"facialcasting", "facialcasting.com", "Facial Casting", nil, nil},
	{"glamourbunnies", "glamourbunnies.com", "Glamour Bunnies", nil, nil},
	// The domain is the only place "Goddess Diane" appears; the site itself
	// brands as Diane Andrews throughout, which is also its StashDB name.
	{"goddessdiane", "goddessdiane.com", "Diane Andrews", []string{"Diane Andrews"}, nil},
	{"lisariveraxo", "lisariveraxo.com", "Lisa Rivera XO", []string{"Lisa Rivera"}, nil},
	{"monstermalesprod", "monstermalesprod.com", "Monster Males", nil, nil},
	{"mugurporn", "mugurporn.com", "Mugur Porn", nil, nil},
	{"naughtycolombia", "naughtycolombia.com", "Naughty Colombia", nil, nil},
	{"nerdsofporn", "nerdsofporn.com", "Nerds of Porn", nil, nil},
	{"peccatriciproduzioni", "peccatriciproduzioni.com", "Peccatrici Produzioni", nil, nil},
	{"pennybarber", "pennybarber.com", "Penny Barber", []string{"Penny Barber"}, nil},
	{"pervsmilfsnteens", "pervsmilfsnteens.com", "Pervs MILFs n Teens", nil, nil},
	{"porntugal", "porntugal.com", "Porntugal", nil, nil},
	{"pvgirls", "pvgirls.com", "Porn Valley Girls", nil, nil},
	{"ricporter", "ricporter.tv", "Ric Porter", nil, nil},
	{"sexyninarivera", "sexyninarivera.com", "Sexy Nina Rivera", []string{"Nina Rivera"}, nil},
	{"sukmydick", "sukmydick.com", "Suk My Dick", nil, nil},
	{"superhotfilms", "superhotfilms.com", "Super Hot Films", nil, nil},
	{"thejerkygirls", "thejerkygirls.com", "The Jerky Girls", nil, nil},
	{"thiccvision", "thiccvision.com", "Thicc Vision", nil, nil},
	{"thicq", "thicq.com", "THICQ", nil, nil},
	{"throatwars", "throatwars.com", "Throat Wars", nil, nil},
	{"wetwetgirls", "wetwetgirls.com", "Wet Wet Girls", nil, nil},
	{"yungdumbsluts", "yungdumbsluts.com", "Yung Dumb Sluts", nil, nil},
	{"rosellaextrem", "rosella-extrem.com", "RosellaExtrem", nil, nil},
	{"thelionxxx", "thelionxxx.com", "The Lion XXX", nil, nil},
	{"kinkyrubberworld", "kinkyrubberworld.com", "Kinky Rubber World", []string{"Latex Lara"}, nil},
	{"naughtylada", "naughty-lada.com", "Naughty Lada", []string{"Naughty Lada"}, []string{"naughtylada.com"}},
	{"aussiefellatioqueens", "aussiefellatioqueens.com", "Aussie Fellatio Queens", nil, nil},
	{"arabellesplayground", "arabellesplayground.com", "Arabelle's Playground", []string{"Arabelle Raphael"}, nil},
	{"samantalily", "samantalily.xxx", "Samanta Lily", []string{"Samanta Lily"}, nil},
	{"jenysmith", "jenysmith.net", "Jeny Smith", []string{"Jeny Smith"}, nil},
	{"rebeccalordproductions", "rebeccalordproductions.com", "Rebecca Lord Productions", nil, nil},
}

// matchReFor accepts the site's own domain plus any alias that redirects to it.
func matchReFor(cfg siteConfig) *regexp.Regexp {
	domains := append([]string{cfg.Domain}, cfg.Aliases...)
	for i, d := range domains {
		domains[i] = strings.ReplaceAll(d, ".", `\.`)
	}
	return regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?(?:%s)(?:/videos)?/?(?:\?.*)?$`, strings.Join(domains, "|")))
}

type siteScraper struct {
	*modelcentroutil.Scraper
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*siteScraper)(nil)

func (s *siteScraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func init() {
	for _, cfg := range sites {
		s := &siteScraper{
			Scraper: modelcentroutil.New(modelcentroutil.SiteConfig{
				SiteID:     cfg.SiteID,
				SiteBase:   "https://" + cfg.Domain,
				StudioName: cfg.StudioName,
				Performers: cfg.Performers,
			}),
			matchRe: matchReFor(cfg),
		}
		scraper.Register(s)
	}
}
