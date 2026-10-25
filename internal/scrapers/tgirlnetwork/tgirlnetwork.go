// Package tgirlnetwork scrapes the TGirl Network tour (tgirl-network.com/tour)
// and the network's solo performer sites.
//
// The solo sites (mandy-mitchell.com, mia-isabella.com, …) are hand-built
// tours that differ from one another and mostly carry undated, truncated
// static thumbnails. Every one of their scenes is also on the network tour,
// under that performer's `/tour/<slug>/` page, in one template. So a solo
// site's URL is scraped through its network model page, and stored under the
// solo URL.
//
// The host offers only DHE and static-RSA key exchange, so requests go through
// httpx.NewLegacyTLSClient.
package tgirlnetwork

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID         = "tgirlnetwork"
	networkDomain  = "tgirl-network.com"
	networkBase    = "https://www.tgirl-network.com"
	networkStudio  = "TGirl Network"
	pageSize       = 18
	defaultWorkers = 4
)

// soloSite maps a performer's own domain to her network model page.
type soloSite struct {
	domain string
	slug   string
	studio string
}

var soloSites = []soloSite{
	{"ashley-george.com", "ashley-george", "Ashley George"},
	{"baileeparis.com", "bailee-paris", "Bailee Paris"},
	{"biankanascimento.com", "bianka-nascimento", "Bianka Nascimento"},
	{"bustykimberxxx.com", "kimber-james", "Busty Kimber XXX"},
	{"evalinxxx.com", "eva-lin", "Eva Lin"},
	{"holly-harlow.com", "holly-harlow", "Holly Harlow"},
	{"jessicathefox.com", "jessica-the-fox", "Jessica The Fox"},
	{"jonellebrooks.com", "jonelle-brooks", "Jonelle Brooks"},
	{"mandy-mitchell.com", "mandy-mitchell", "Mandy Mitchell"},
	{"mia-isabella.com", "mia-isabella", "Mia Isabella"},
	{"nikkimontero.com", "nikki-montero", "Nikki Montero"},
	{"pennytylerxxx.com", "penny-tyler", "Penny Tyler XXX"},
	{"sarinavalentina.com", "sarina-valentina", "Sarina Valentina"},
	{"tiffanystarrxxx.com", "tiffany-starr", "Tiffany Starr"},
	{"ts-baileyjay.com", "bailey-jay", "Bailey Jay"},
	{"ts-kimberjames.com", "kimber-james", "Club Kimber James"},
	{"ts-kimberlee.com", "kimber-lee", "TS Kimberlee"},
}

// networkAliases are brands that redirect into the whole network listing.
var networkAliases = []string{"tgirlpinups.com"}

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewLegacyTLSClient(30 * time.Second), base: networkBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

var (
	hostRe       = regexp.MustCompile(`^https?://(?:www\.)?([^/?#:]+)`)
	modelPathRe  = regexp.MustCompile(`^https?://(?:www\.)?tgirl-network\.com/tour/([a-z0-9-]+)/?(?:[?#]|$)`)
	modelParamRe = regexp.MustCompile(`[?&]model=([A-Za-z0-9_-]+)`)
)

// reservedPaths are tour sections, not model pages.
var reservedPaths = map[string]bool{
	"movie-updates": true, "picture-updates": true, "the-tgirls": true, "join": true,
}

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	p := []string{
		networkDomain,
		networkDomain + "/tour/",
		networkDomain + "/tour/{slug}/",
		networkDomain + "/tour/index.php?model={model}",
		networkDomain + "/tour/index.php?section={section}",
	}
	p = append(p, networkAliases...)
	for _, so := range soloSites {
		p = append(p, so.domain)
	}
	return p
}

func hostOf(u string) string {
	if m := hostRe.FindStringSubmatch(u); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

func soloFor(host string) (soloSite, bool) {
	for _, so := range soloSites {
		if so.domain == host {
			return so, true
		}
	}
	return soloSite{}, false
}

func (s *Scraper) MatchesURL(u string) bool {
	host := hostOf(u)
	if host == networkDomain {
		return true
	}
	for _, a := range networkAliases {
		if host == a {
			return true
		}
	}
	_, ok := soloFor(host)
	return ok
}

// RecognizesURL vouches for any path on a solo or alias domain: each one is a
// single performer (or the whole network) whatever page of its tour is named,
// so the path is never dispatched on.
func (s *Scraper) RecognizesURL(u string) bool {
	host := hostOf(u)
	for _, a := range networkAliases {
		if host == a {
			return true
		}
	}
	_, ok := soloFor(host)
	return ok
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// target is what a studio URL resolves to on the network tour.
type target struct {
	slug   string // /tour/<slug>/ model page
	model  string // index.php?model= value, when the URL names it directly
	studio string
}

func resolve(studioURL string) target {
	host := hostOf(studioURL)
	if so, ok := soloFor(host); ok {
		return target{slug: so.slug, studio: so.studio}
	}
	if host == networkDomain {
		if m := modelParamRe.FindStringSubmatch(studioURL); m != nil {
			return target{model: m[1], studio: networkStudio}
		}
		if m := modelPathRe.FindStringSubmatch(studioURL); m != nil && !reservedPaths[m[1]] {
			return target{slug: m[1], studio: networkStudio}
		}
	}
	return target{studio: networkStudio}
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	t := resolve(studioURL)
	switch {
	case t.slug != "":
		scraper.Debugf(1, "%s: scraping model page %q", siteID, t.slug)
	case t.model != "":
		scraper.Debugf(1, "%s: scraping model %q", siteID, t.model)
	default:
		scraper.Debugf(1, "%s: scraping the network video listing", siteID)
	}

	model := t.model
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		var path string
		switch {
		case t.slug != "" && page == 1:
			path = "/tour/" + t.slug + "/"
		case t.slug != "" || t.model != "":
			if model == "" {
				return scraper.PageResult{Done: true}, nil
			}
			path = fmt.Sprintf("/tour/index.php?model=%s&start=%d", url.QueryEscape(model), (page-1)*pageSize)
		default:
			path = fmt.Sprintf("/tour/index.php?gtype=videos&start=%d", (page-1)*pageSize)
		}

		body, err := s.fetch(ctx, s.base+path)
		if err != nil {
			return scraper.PageResult{}, err
		}
		page1 := string(body)
		if t.slug != "" && page == 1 {
			// The slug and the listing's model key differ (mandy-mitchell →
			// mandy), and only the page's own pagination links name the key.
			if m := modelParamRe.FindStringSubmatch(page1); m != nil {
				model = m[1]
			}
		}

		cards, items := parseCards(page1)
		if items == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(s.base+path, fmt.Errorf("no update cards on the first listing page"))
			}
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.buildScenes(ctx, studioURL, t.studio, cards, opts, out)
		return scraper.PageResult{
			Scenes:   scenes,
			Done:     items < pageSize,
			Continue: true,
		}, nil
	})
}

// buildScenes fetches each new card's detail page for performer and
// description. A known scene is returned card-only; Paginate skips it anyway.
// A failed detail costs those two fields, not the scene.
func (s *Scraper) buildScenes(ctx context.Context, studioURL, studio string, cards []card, opts scraper.ListOpts, out chan<- scraper.SceneResult) []models.Scene {
	now := time.Now().UTC()
	scenes := make([]models.Scene, len(cards))
	for i, c := range cards {
		scenes[i] = models.Scene{
			ID:        c.id,
			SiteID:    siteID,
			StudioURL: studioURL,
			Title:     c.title,
			URL:       s.base + "/tour/show_video.php?galid=" + c.id + "&section=" + c.section,
			Thumbnail: s.absolute(c.thumb),
			Studio:    studio,
			ScrapedAt: now,
		}
	}

	workers := scraper.WorkerCount(opts, defaultWorkers)
	idx := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				if err := s.enrich(ctx, &scenes[i]); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		}()
	}
feed:
	for i, c := range cards {
		if opts.KnownIDs[c.id] {
			continue
		}
		select {
		case idx <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(idx)
	wg.Wait()

	for _, err := range errs {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
			return scenes
		}
	}
	return scenes
}

func (s *Scraper) enrich(ctx context.Context, sc *models.Scene) error {
	body, err := s.fetch(ctx, sc.URL)
	if err != nil {
		return fmt.Errorf("detail %s: %w", sc.ID, err)
	}
	d := parseDetail(string(body))
	if d.title == "" {
		return scraper.ParseError(sc.URL, fmt.Errorf("no title on detail page"))
	}
	sc.Title = d.title
	sc.Description = d.description
	sc.Performers = d.performers
	return nil
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		Method:  http.MethodGet,
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func (s *Scraper) absolute(ref string) string {
	switch {
	case ref == "":
		return ""
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return ref
	case strings.HasPrefix(ref, "/"):
		return s.base + ref
	default:
		return s.base + "/tour/" + ref
	}
}

// ---- listing ----

type card struct {
	id      string
	section string
	title   string
	thumb   string
}

var (
	cardSplitRe = regexp.MustCompile(`<div class="uthumb-item">`)
	videoLinkRe = regexp.MustCompile(`show_video\.php\?galid=(\d+)&(?:amp;)?section=(\d+)`)
	galLinkRe   = regexp.MustCompile(`show_gal\.php\?galid=\d+`)
	cardAltRe   = regexp.MustCompile(`<img[^>]+alt="([^"]*)"`)
	cardTextRe  = regexp.MustCompile(`(?s)<div class="title-date">.*?<a[^>]*>(.*?)</a>`)
	cardThumbRe = regexp.MustCompile(`background:\s*url\(([^)]+)\)`)
)

// parseCards returns the video cards of a listing page and the number of
// update cards of any kind on it. Model pages mix photo galleries in, so the
// raw count is what decides whether the listing has ended.
func parseCards(body string) ([]card, int) {
	chunks := cardSplitRe.Split(body, -1)
	var out []card
	items := 0
	seen := make(map[string]bool)
	for _, chunk := range chunks[1:] {
		m := videoLinkRe.FindStringSubmatch(chunk)
		if m == nil {
			if galLinkRe.MatchString(chunk) {
				items++
			}
			continue
		}
		items++
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		// The link text is cut to ~20 characters; the play button's alt is not.
		title := cleanText(firstSubmatch(cardAltRe, chunk))
		if title == "" {
			title = cleanText(firstSubmatch(cardTextRe, chunk))
		}
		out = append(out, card{
			id:      m[1],
			section: m[2],
			title:   title,
			thumb:   strings.Trim(firstSubmatch(cardThumbRe, chunk), `'"`),
		})
	}
	return out, items
}

// ---- detail ----

type detail struct {
	title       string
	description string
	performers  []string
}

var (
	detailTitleRe    = regexp.MustCompile(`(?s)<h1 class="galtitle[^"]*">(.*?)</h1>`)
	detailDescRe     = regexp.MustCompile(`(?s)<p class="videodesc">(.*?)</p>`)
	detailKeywordsRe = regexp.MustCompile(`<meta name="keywords" content="([^"]*)"`)
	detailPresentsRe = regexp.MustCompile(`(?s)<div class="galpres">.*?Presents(.*?)In\s*<`)
)

func parseDetail(body string) detail {
	d := detail{
		title:       cleanText(firstSubmatch(detailTitleRe, body)),
		description: cleanText(firstSubmatch(detailDescRe, body)),
	}
	// The keywords meta is "tgirl network, <performer>[, <performer>…]"; the
	// performers come from the section the update was filed under.
	for _, k := range strings.Split(html.UnescapeString(firstSubmatch(detailKeywordsRe, body)), ",") {
		k = strings.TrimSpace(k)
		if k == "" || strings.EqualFold(k, "tgirl network") {
			continue
		}
		d.performers = append(d.performers, k)
	}
	if len(d.performers) == 0 {
		if p := cleanText(firstSubmatch(detailPresentsRe, body)); p != "" {
			d.performers = []string{p}
		}
	}
	return d
}

// ---- helpers ----

var tagStripRe = regexp.MustCompile(`<[^>]*>`)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}
