package videoelements

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Anastylosis/FSS/internal/scrapers/veutil"
	"github.com/Anastylosis/FSS/scraper"
)

type siteConfig struct {
	SiteID         string
	Domain         string
	StudioName     string
	MainCategoryID int
}

var sites = []siteConfig{
	{"boyfriendsharing", "boyfriendsharing.com", "ShareMyBF", 1},
	{"brattyfamily", "brattyfamily.com", "BrattyFamily", 1},
	{"gostuckyourself", "gostuckyourself.net", "GoStuckYourself", 1},
	{"hugecockbreak", "hugecockbreak.com", "HugeCockBreak", 1},
	{"littlefromasia", "littlefromasia.com", "LittleFromAsia", 1},
	{"mommysboy", "mommysboy.net", "MommysBoy", 1},
	{"momxxx", "momxxx.org", "MomXXX", 10},
	{"mybadmilfs", "mybadmilfs.com", "MyBadMILFs", 1},
	{"mydaughterswap", "mydaughterswap.com", "DaughterSwap", 1},
	{"mypervmom", "mypervmom.com", "PervMom", 1},
	{"mysislovesme", "mysislovesme.com", "SisLovesMe", 1},
	{"youngerloverofmine", "youngerloverofmine.com", "YoungerLoverOfMine", 1},
	{"girlswholie", "girlswholie.tube", "GirlsWhoLie", 1},
	{"abuseme", "abuseme.net", "Abuse Me", 1},
}

func init() {
	for _, cfg := range sites {
		escaped := strings.ReplaceAll(cfg.Domain, ".", `\.`)
		re := regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?%s(/|$)`, escaped))

		s := veutil.New(veutil.SiteConfig{
			ID:             cfg.SiteID,
			Studio:         cfg.StudioName,
			SiteBase:       "https://" + cfg.Domain,
			MainCategoryID: cfg.MainCategoryID,
			Patterns:       []string{cfg.Domain},
			MatchRe:        re,
		})
		scraper.Register(s)
	}
}
