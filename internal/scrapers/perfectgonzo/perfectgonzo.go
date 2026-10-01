// Package perfectgonzo registers the Perfect Gonzo network sites. All eight run
// the same custom PHP CMS — see perfectgonzoutil.
package perfectgonzo

import (
	"regexp"

	"github.com/Anastylosis/FSS/internal/scrapers/perfectgonzoutil"
	"github.com/Anastylosis/FSS/scraper"
)

func site(id, host, studio string) perfectgonzoutil.SiteConfig {
	return perfectgonzoutil.SiteConfig{
		ID:       id,
		SiteBase: "http://www." + host,
		Studio:   studio,
		Patterns: []string{host, host + "/movies", host + "/movies/page-{N}/"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(host) + `(?:/|$)`),
	}
}

var sites = []perfectgonzoutil.SiteConfig{
	site("allinternal", "allinternal.com", "All Internal"),
	site("asstraffic", "asstraffic.com", "Ass Traffic"),
	site("spermswap", "spermswap.com", "Sperm Swap"),
	site("primecups", "primecups.com", "Prime Cups"),
	site("tamedteens", "tamedteens.com", "Tamed Teens"),
	site("cumforcover", "cumforcover.com", "Cum For Cover"),
	site("milfthing", "milfthing.com", "Milf Thing"),
	site("purepov", "purepov.com", "Pure POV"),
}

func init() {
	for _, cfg := range sites {
		scraper.Register(perfectgonzoutil.New(cfg))
	}
}
