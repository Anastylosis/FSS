// Package ladyboygold registers ladyboygold.com and its sister sites. The
// network runs the
// same TooMuchMedia NATS tour CMS as Cosplayground and the Mars Media network
// (Angular SPA at `/natscms-app/`, `tour_api.php` JSON API), on the shared
// Island Dollars backend. All discovery and parsing is the natscmsutil core;
// this package only supplies the site's SiteConfig.
//
// cms_area_id and natsUrl come from
// https://www.ladyboygold.com/natscms-app/config.json.
//
// One site-specific wrinkle: the set_list block mixes photo sets into the same
// response as videos — about 1800 of ~5000 entries — with no type field to tell
// them apart. The content `path` is the only signal, so SkipPathRe drops any
// set filed under a photo directory (`photos`, `4kphotos`, `photos4k`,
// `candid_photos`).
package ladyboygold

import (
	"regexp"
	"strings"

	"github.com/Anastylosis/FSS/internal/scrapers/natscmsutil"
	"github.com/Anastylosis/FSS/scraper"
)

// skipPathRe matches the photo-set directories in the content path, e.g.
// "lbg/00lfb/4kphotos/some_set/". Video directories (videos, 4kvideos,
// videos4k, remastered4k, ladyboys, …) are kept.
var skipPathRe = regexp.MustCompile(`/[0-9a-z_]*photos[0-9a-z_]*/`)

func New() *natscmsutil.Scraper {
	return natscmsutil.New(natscmsutil.SiteConfig{
		ID:          "ladyboygold",
		SiteBase:    "https://ladyboygold.com",
		SiteName:    "Ladyboy Gold",
		StudioName:  "Ladyboy Gold",
		NatsAPIBase: "https://nats.islanddollars.com/tour_api.php",
		CMSAreaID:   "cd9a5600-5cda-4ed0-b356-f62af1887d96",
		SkipPathRe:  skipPathRe,
		Patterns:    []string{"ladyboygold.com"},
		MatchRe:     regexp.MustCompile(`^https?://(?:www\.)?ladyboygold\.com(?:/|$)`),
	})
}

// natsAPIBase is the shared Island Dollars backend behind every site in the
// network; per-site scoping comes from the CMSAreaID header.
const natsAPIBase = "https://nats.islanddollars.com/tour_api.php"

// siblings are the rest of the network, each on the same backend with its own
// cms_area_id (read from that site's /natscms-app/config.json). They share
// ladyboygold.com's photo-set problem, so they share skipPathRe.
//
// Two children of the stashdb tree are absent: ladyboyobsession.com and
// ladyboydildo.com no longer resolve, and Ladyboy Obsession's other listed URL
// is ladyboyvice.com, which is covered. Studio is set per site rather than to
// the network name, because stashdb tracks them as separate studios.
var siblings = []natscmsutil.SiteConfig{
	{
		ID:        "tsraw",
		SiteBase:  "https://www.tsraw.com",
		SiteName:  "TSRaw",
		CMSAreaID: "cc6bd0ac-a417-47d1-9868-7855b25986e5",
		MatchRe:   regexp.MustCompile(`^https?://(?:www\.)?tsraw\.com(?:/|$)`),
	},
	{
		ID:        "ladyboycrush",
		SiteBase:  "https://www.ladyboycrush.com",
		SiteName:  "Ladyboy Crush",
		CMSAreaID: "74175374-c756-4ae9-97b2-e011512a1521",
		MatchRe:   regexp.MustCompile(`^https?://(?:www\.)?ladyboycrush\.com(?:/|$)`),
	},
	{
		ID:        "ladyboyvice",
		SiteBase:  "https://www.ladyboyvice.com",
		SiteName:  "Ladyboy Vice",
		CMSAreaID: "c594b28c-ab09-44da-9166-0a332d33469f",
		MatchRe:   regexp.MustCompile(`^https?://(?:www\.)?ladyboyvice\.com(?:/|$)`),
	},
	{
		ID:        "ladyboypussy",
		SiteBase:  "https://www.ladyboypussy.com",
		SiteName:  "Ladyboy Pussy",
		CMSAreaID: "3b74725d-ad01-45a1-8186-ac6be1bc1661",
		MatchRe:   regexp.MustCompile(`^https?://(?:www\.)?ladyboypussy\.com(?:/|$)`),
	},
	{
		ID:        "ladyboysfuckedbareback",
		SiteBase:  "https://www.ladyboysfuckedbareback.com",
		SiteName:  "Ladyboys Fucked Bareback",
		CMSAreaID: "126f96ec-ffdc-4f4b-a459-c9a2e78b9b67",
		MatchRe:   regexp.MustCompile(`^https?://(?:www\.)?ladyboysfuckedbareback\.com(?:/|$)`),
	},
}

// withDefaults fills the shared backend, the photo-set filter and the derived
// display fields onto a sibling row.
func withDefaults(cfg natscmsutil.SiteConfig) natscmsutil.SiteConfig {
	cfg.NatsAPIBase = natsAPIBase
	cfg.SkipPathRe = skipPathRe
	cfg.StudioName = cfg.SiteName
	cfg.Patterns = []string{strings.TrimPrefix(cfg.SiteBase, "https://")}
	return cfg
}

func init() {
	scraper.Register(New())
	for _, cfg := range siblings {
		scraper.Register(natscmsutil.New(withDefaults(cfg)))
	}
}
