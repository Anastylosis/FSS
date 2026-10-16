package empirestore

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Anastylosis/FSS/internal/scrapers/empirestoreutil"
	"github.com/Anastylosis/FSS/scraper"
)

type siteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	ListingURL string
}

var sites = []siteConfig{
	{"elegantangel", "elegantangel.com", "Elegant Angel", "/watch-newest-elegant-angel-clips-and-scenes.html"},
	{"vouyermedia", "vouyermedia.com", "Vouyer Media", "/watch-newest-vouyer-media-clips-and-scenes.html"},
	{"reaganfoxx", "reaganfoxx.com", "Reagan Foxx", "/scenes/673608/reagan-foxx-streaming-pornstar-videos.html"},
	{"jodiwest", "jodiwest.com", "Jodi West", "/watch-newest-jodi-west-clips-and-scenes.html"},
	{"bobsvideos", "bobsvideos.empirestores.co", "Bob's Videos", "/shop-streaming-video-by-scene.html"},
	{"justinslayer", "jsi.empirestores.co", "Justin Slayer International", "/shop-streaming-video-by-scene.html"},
	{"spungygunkfilms", "spungygunkfilms.empirestores.co", "Spungy Gunk Films", "/shop-streaming-video-by-scene.html"},
	{"dvsx", "dvsx.com", "DVSX", "/watch-streaming-video-by-scene.html?studio=21558&sort=released"},
}

type siteScraper struct {
	es     *empirestoreutil.Scraper
	config siteConfig
	re     *regexp.Regexp
}

var _ scraper.StudioScraper = (*siteScraper)(nil)

func (s *siteScraper) ID() string         { return s.config.SiteID }
func (s *siteScraper) Patterns() []string { return []string{s.config.Domain + "/"} }
func (s *siteScraper) MatchesURL(u string) bool {
	return s.re.MatchString(u)
}

func (s *siteScraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.es.Run(ctx, studioURL, opts, out)
	return out, nil
}

func init() {
	for _, cfg := range sites {
		escaped := strings.ReplaceAll(cfg.Domain, ".", `\.`)
		re := regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?%s(?:/|$)`, escaped))
		s := &siteScraper{
			es: empirestoreutil.New(empirestoreutil.SiteConfig{
				SiteID:     cfg.SiteID,
				Domain:     cfg.Domain,
				StudioName: cfg.StudioName,
				ListingURL: cfg.ListingURL,
			}),
			config: cfg,
			re:     re,
		}
		scraper.Register(s)
	}
}
