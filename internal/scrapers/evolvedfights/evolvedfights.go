// Package evolvedfights scrapes ElevatedX tour sites that use the "latestUpdateB"
// card template (data-setid cards, /scenes/{slug}_vids.html detail URLs, a
// videoInfo block carrying the date and runtime). Evolved Fights and Candy
// Glitter share this exact template. (Evolved Fights Lez uses the older
// updateItem template and is handled by darkreachupdateitemutil instead.)
package evolvedfights

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type siteConfig struct {
	SiteID  string
	Studio  string
	Base    string // e.g. https://evolvedfights.com (no trailing slash)
	Listing string // listing stem: "updates" or "movies"
	Sort    string // sort suffix: "p" (popular) or "d" (date)
}

var sites = []siteConfig{
	{"evolvedfights", "Evolved Fights", "https://evolvedfights.com", "updates", "p"},
	{"candyglitter", "Candy Glitter", "https://candyglitterclips.com", "movies", "d"},
}

const detailWorkers = 6

type Scraper struct {
	cfg     siteConfig
	client  *http.Client
	matchRe *regexp.Regexp
	base    string // overridable for tests
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func newScraper(cfg siteConfig) *Scraper {
	host := strings.TrimPrefix(strings.TrimPrefix(cfg.Base, "https://"), "http://")
	escaped := strings.ReplaceAll(host, ".", `\.`)
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + escaped + `(?:/|$)`),
		base:    cfg.Base,
	}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newScraper(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	host := strings.TrimPrefix(strings.TrimPrefix(s.cfg.Base, "https://"), "http://")
	return []string{host, host + "/categories/" + s.cfg.Listing + "_{N}_" + s.cfg.Sort + ".html"}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		cards, err := s.fetchListing(ctx, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if len(cards) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), cards, now)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func (s *Scraper) listingURL(page int) string {
	return fmt.Sprintf("%s/categories/%s_%d_%s.html", s.base, s.cfg.Listing, page, s.cfg.Sort)
}

func (s *Scraper) fetchListing(ctx context.Context, page int) ([]card, error) {
	u := s.listingURL(page)
	scraper.Debugf(1, "%s: fetching listing page %d", s.cfg.SiteID, page)
	body, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}
	return parseListing(body), nil
}

// enrich fetches each card's detail page (date/duration/description) with a
// bounded worker pool, then builds the scenes. Detail failures fall back to the
// listing data so a transient error never drops a scene.
func (s *Scraper) enrich(ctx context.Context, workers int, cards []card, now time.Time) []models.Scene {
	scenes := make([]models.Scene, len(cards))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				scenes[idx] = s.toScene(ctx, cards[idx], now)
			}
		}()
	}
	for i := range cards {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return scenes[:0]
		}
	}
	close(jobs)
	wg.Wait()
	return scenes
}

func (s *Scraper) toScene(ctx context.Context, c card, now time.Time) models.Scene {
	sc := models.Scene{
		ID:         c.id,
		SiteID:     s.cfg.SiteID,
		StudioURL:  s.cfg.Base,
		Title:      c.title,
		URL:        c.url,
		Studio:     s.cfg.Studio,
		Thumbnail:  s.absURL(c.thumb),
		Performers: c.performers,
		ScrapedAt:  now,
	}
	if body, err := s.get(ctx, s.rebase(c.url)); err == nil {
		if d := parseDetailDate(body); !d.IsZero() {
			sc.Date = d
		}
		sc.Duration = parseDetailDuration(body)
		sc.Description = parseDetailDescription(body)
	} else {
		scraper.Debugf(1, "%s: detail fetch failed for %s: %v", s.cfg.SiteID, c.url, err)
	}
	return sc
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

type card struct {
	id         string
	title      string
	url        string
	thumb      string
	performers []string
}

var (
	cardSplitRe = regexp.MustCompile(`class="latestUpdateB"`)
	setIDRe     = regexp.MustCompile(`data-setid="(\d+)"`)
	titleRe     = regexp.MustCompile(`(?s)<h4 class="link_bright">\s*<a[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	thumbRe     = regexp.MustCompile(`src0_1x="([^"]+)"`)
	infoLinkRe  = regexp.MustCompile(`<a class="link_bright infolink"[^>]*>([^<]+)</a>`)
	vidsURLRe   = regexp.MustCompile(`/scenes/[^"]+_vids\.html`)
)

func parseListing(body []byte) []card {
	html := string(body)
	idxs := cardSplitRe.FindAllStringIndex(html, -1)
	var cards []card
	seen := map[string]bool{}
	for i, loc := range idxs {
		end := len(html)
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		}
		block := html[loc[0]:end]
		c, ok := parseCard(block)
		if !ok || seen[c.id] {
			continue
		}
		seen[c.id] = true
		cards = append(cards, c)
	}
	return cards
}

func parseCard(block string) (card, bool) {
	tm := titleRe.FindStringSubmatch(block)
	if tm == nil {
		return card{}, false
	}
	url := htmlText(tm[1])
	if !vidsURLRe.MatchString(url) {
		return card{}, false // skip DVD/photo/cart cards
	}
	c := card{
		url:   url,
		title: htmlText(tm[2]),
	}
	if m := setIDRe.FindStringSubmatch(block); m != nil {
		c.id = m[1]
	} else {
		c.id = slugFromVids(url)
	}
	if m := thumbRe.FindStringSubmatch(block); m != nil {
		c.thumb = m[1] // resolved against the site base in toScene
	}
	for _, m := range infoLinkRe.FindAllStringSubmatch(block, -1) {
		name := htmlText(m[1])
		if name != "" {
			c.performers = append(c.performers, name)
		}
	}
	return c, true
}

var (
	videoInfoRe = regexp.MustCompile(`(?s)<ul class="videoInfo">(.*?)</ul>`)
	dateRe      = regexp.MustCompile(`(\d{2}/\d{2}/\d{4})`)
	durationRe  = regexp.MustCompile(`(\d+)\s*min`)
	descRe      = regexp.MustCompile(`(?s)<div class="updateInfo">(.*?)</div>`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

func parseDetailDate(body []byte) time.Time {
	info := videoInfoBlock(body)
	if m := dateRe.FindStringSubmatch(info); m != nil {
		if d, err := time.Parse("01/02/2006", m[1]); err == nil {
			return d.UTC()
		}
	}
	return time.Time{}
}

func parseDetailDuration(body []byte) int {
	info := videoInfoBlock(body)
	if m := durationRe.FindStringSubmatch(info); m != nil {
		if mins, err := strconv.Atoi(m[1]); err == nil {
			return mins * 60
		}
	}
	return 0
}

func videoInfoBlock(body []byte) string {
	if m := videoInfoRe.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

func parseDetailDescription(body []byte) string {
	if m := descRe.FindSubmatch(body); m != nil {
		return htmlText(tagStripRe.ReplaceAllString(string(m[1]), " "))
	}
	return ""
}

func htmlText(s string) string {
	return strings.TrimSpace(html.UnescapeString(tagStripRe.ReplaceAllString(s, "")))
}

func slugFromVids(u string) string {
	i := strings.LastIndex(u, "/scenes/")
	if i < 0 {
		return u
	}
	s := u[i+len("/scenes/"):]
	return strings.TrimSuffix(s, "_vids.html")
}

// rebase rewrites an absolute scene URL's scheme+host onto s.base so the
// detail fetch hits the configured (or test) origin, not the hardcoded domain.
func (s *Scraper) rebase(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		if slash := strings.IndexByte(u[i+3:], '/'); slash >= 0 {
			return s.base + u[i+3+slash:]
		}
	}
	return u
}

func (s *Scraper) absURL(p string) string {
	if p == "" || strings.HasPrefix(p, "http") {
		return p
	}
	return s.cfg.Base + ensureSlash(p)
}

func ensureSlash(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}
