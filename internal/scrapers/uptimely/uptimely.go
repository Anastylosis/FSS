package uptimely

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Anastylosis/FSS/internal/scrapers/uptimelyutil"
	"github.com/Anastylosis/FSS/scraper"
)

type siteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

var sites = []siteConfig{
	{"attackers", "attackers.net", "Attackers"},
	{"chijoheaven", "bi-av.com", "Chijo Heaven"},
	{"dasdas", "dasdas.jp", "DAS!"},
	{"ebody", "av-e-body.com", "E-body"},
	{"fitch", "fitch-av.com", "Fitch"},
	{"hhhgroup", "hhh-av.com", "HHH Group"},
	{"honnaka", "honnaka.jp", "Honnaka"},
	{"ideapocket", "ideapocket.com", "Idea Pocket"},
	{"kawaii", "kawaiikawaii.jp", "Kawaii"},
	{"kirakira", "kirakira-av.com", "Kira Kira"},
	{"madonna", "madonna-av.com", "Madonna"},
	{"moodyz", "moodyz.com", "MOODYZ"},
	{"oppai", "oppai-av.com", "Oppai"},
	{"s1no1style", "s1s1s1.com", "S1 NO.1 STYLE"},
	{"tameikegoro", "tameikegoro.jp", "Tameike Goro"},
	{"wanzfactory", "wanz-factory.com", "Wanz Factory"},
}

// matchRe builds a site's URL matcher. The host itself is the whole-catalogue
// entry point; the narrower `/works/...` and `/actress/...` paths address one
// view of it. Matching the bare host is what lets `fss scrape https://<domain>/`
// reach the genre-index traversal.
func matchRe(domain string) *regexp.Regexp {
	escaped := strings.ReplaceAll(domain, ".", `\.`)
	return regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?%s(?:/|$)`, escaped))
}

func init() {
	for _, cfg := range sites {
		s := uptimelyutil.New(uptimelyutil.SiteConfig{
			ID:     cfg.SiteID,
			Studio: cfg.StudioName,
			Domain: cfg.Domain,
			Patterns: []string{
				cfg.Domain,
				cfg.Domain + "/works/list/series/{id}",
				cfg.Domain + "/works/list/release",
				cfg.Domain + "/works/list/date/{date}",
				cfg.Domain + "/works/list/genre/{id}",
				cfg.Domain + "/works/list/label/{id}",
				cfg.Domain + "/actress/detail/{id}",
			},
			MatchRe: matchRe(cfg.Domain),
		})
		scraper.Register(s)
	}
}
