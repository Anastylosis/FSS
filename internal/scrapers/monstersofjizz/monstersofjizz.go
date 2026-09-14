// Package monstersofjizz registers monstersofjizz.com, an Elevated X
// "updateItem" tour (see elxupdateutil) at the site root with its catalogue
// under the "updates" slug.
package monstersofjizz

import (
	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

var site = elxupdateutil.SiteConfig{
	SiteID:     "monstersofjizz",
	Domain:     "monstersofjizz.com",
	StudioName: "Monsters of Jizz",
	ListSlug:   "updates",
	BareHost:   true,
}

func init() { scraper.Register(elxupdateutil.New(site)) }
