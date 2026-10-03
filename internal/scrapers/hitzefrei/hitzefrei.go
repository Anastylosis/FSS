// Package hitzefrei scrapes tour.hitzefrei.com and its sub-brand channels. See
// docs/scrapers.md.
package hitzefrei

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "hitzefrei"
	defaultStudio = "Hitzefrei"
	detailWorkers = 4
)

// brands maps the tour's site=N filter to the sub-brand name.
var brands = map[string]string{
	"1":  "Patti's Anals",
	"2":  "Family Affairs",
	"3":  "City Check",
	"5":  "Fuck On Arrival",
	"7":  "Milf Hunters",
	"8":  "Cuff 'em All",
	"9":  "fANALarm",
	"10": "Unleashed",
}

// brandHosts maps each sub-brand subdomain to its site=N id.
var brandHosts = map[string]string{
	"pattisanals":   "1",
	"familyaffairs": "2",
	"citycheck":     "3",
	"fuckonarrival": "5",
	"milfhunters":   "7",
	"cuffemall":     "8",
	"fanalarm":      "9",
	"unleashed":     "10",
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:(?:www|tour|[a-z]+)\.)?hitzefrei\.com(?:[/?#]|$)`)
	modelRe = regexp.MustCompile(`^/model/(\d+(?:/[^/?#]+)?)/?$`)

	cardSplitRe = regexp.MustCompile(`class="[^"]*(?:content-hitem|container-large-video-thumb)[^"]*"`)
	titleRe     = regexp.MustCompile(`(?s)<h1 class="content-title">\s*<a href="[^"]*?/view/(\d+)/([^"?#]*)"[^>]*>(.*?)</a>`)
	thumbRe     = regexp.MustCompile(`<img src="([^"]+)"`)
	logoRe      = regexp.MustCompile(`site-logos/(\d+)-`)
	modelsRe    = regexp.MustCompile(`(?s)<p class="content-models"[^>]*>(.*?)</p>`)
	modelLinkRe = regexp.MustCompile(`(?s)<a href="[^"]*/model/[^"]*"[^>]*>(.*?)</a>`)
	siteLinkRe  = regexp.MustCompile(`(?s)<p class="content-site">.*?<a [^>]*>(.*?)</a>`)
	dateRe      = regexp.MustCompile(`\d{2}/\d{2}/\d{4}`)
	runtimeRe   = regexp.MustCompile(`fa-video"></span>\s*(\d+(?::\d+)+)`)

	metaRe   = regexp.MustCompile(`class="meta-value">([^<]*)<`)
	descRe   = regexp.MustCompile(`(?s)<div class="content-description">(.*?)</div>`)
	tagStrip = regexp.MustCompile(`<[^>]+>`)
)

const dateLayout = "02/01/2006"

// Scraper implements scraper.StudioScraper for the Hitzefrei tour.
type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://tour.hitzefrei.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"tour.hitzefrei.com/videos",
		"tour.hitzefrei.com/videos?site={id}",
		"tour.hitzefrei.com/model/{id}/{slug}",
		"{brand}.hitzefrei.com",
		"hitzefrei.com",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// listing is what an operator URL selects: the whole tour, one sub-brand, or
// one model's single page.
type listing struct {
	site  string
	model string
}

func resolveListing(studioURL string) listing {
	u, err := url.Parse(studioURL)
	if err != nil {
		return listing{}
	}
	if m := modelRe.FindStringSubmatch(u.Path); m != nil {
		return listing{model: m[1]}
	}
	if id := u.Query().Get("site"); brands[id] != "" {
		return listing{site: id}
	}
	if sub, _, ok := strings.Cut(strings.ToLower(u.Hostname()), "."); ok {
		if id, ok := brandHosts[sub]; ok {
			return listing{site: id}
		}
	}
	return listing{}
}

func (s *Scraper) listingURL(l listing, page int) string {
	if l.model != "" {
		return fmt.Sprintf("%s/model/%s?_lang=en", s.base, l.model)
	}
	q := url.Values{"_lang": {"en"}, "page": {strconv.Itoa(page)}}
	if l.site != "" {
		q.Set("site", l.site)
	}
	return s.base + "/videos?" + q.Encode()
}

type card struct {
	id     string
	path   string
	title  string
	thumb  string
	studio string
	cast   []string
	date   time.Time
	secs   int
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	l := resolveListing(studioURL)
	scraper.Debugf(1, "%s: listing %+v", siteID, l)

	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(l, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 && l.model == "" {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}

		fresh := cards[:0]
		for _, c := range cards {
			if !seen[c.id] {
				seen[c.id] = true
				fresh = append(fresh, c)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		enrichTo := len(fresh)
		for i, c := range fresh {
			if opts.KnownIDs[c.id] {
				enrichTo = i
				break
			}
		}
		scenes := make([]models.Scene, len(fresh))
		for i, c := range fresh {
			scenes[i] = s.baseScene(c, studioURL, now)
		}
		s.enrich(ctx, scenes[:enrichTo], opts, out)
		return scraper.PageResult{Scenes: scenes, Done: l.model != ""}, nil
	})
}

func parseListing(body string) []card {
	marks := cardSplitRe.FindAllStringIndex(body, -1)
	var cards []card
	for i, m := range marks {
		end := len(body)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		block := body[m[0]:end]
		t := titleRe.FindStringSubmatch(block)
		if t == nil {
			continue
		}
		c := card{id: t[1], path: "/view/" + t[1] + "/" + t[2], title: cleanText(t[3])}
		for _, im := range thumbRe.FindAllStringSubmatch(block, -1) {
			if !strings.Contains(im[1], "site-logos") {
				c.thumb = html.UnescapeString(im[1])
				break
			}
		}
		if lg := logoRe.FindStringSubmatch(block); lg != nil {
			c.studio = brands[lg[1]]
		}
		if c.studio == "" {
			if sl := siteLinkRe.FindStringSubmatch(block); sl != nil {
				c.studio = cleanText(sl[1])
			}
		}
		if mb := modelsRe.FindStringSubmatch(block); mb != nil {
			for _, ml := range modelLinkRe.FindAllStringSubmatch(mb[1], -1) {
				if name := cleanText(ml[1]); name != "" {
					c.cast = append(c.cast, name)
				}
			}
		}
		if d := dateRe.FindString(block); d != "" {
			if parsed, err := parseutil.TryParseDate(d, dateLayout); err == nil {
				c.date = parsed
			}
		}
		if r := runtimeRe.FindStringSubmatch(block); r != nil {
			c.secs = parseutil.ParseDurationColon(r[1])
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	studio := c.studio
	if studio == "" {
		studio = defaultStudio
	}
	return models.Scene{
		ID:         c.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     studio,
		Title:      c.title,
		URL:        s.base + c.path,
		Thumbnail:  c.thumb,
		Performers: c.cast,
		Date:       c.date,
		Duration:   c.secs,
		ScrapedAt:  now,
	}
}

// enrich fetches each scene's page for the description, and for the runtime
// and date the model page's cards omit.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(scenes) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range scenes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetch(ctx, scenes[i].URL+"?_lang=en")
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(&scenes[i], body)
		}(i)
	}
	wg.Wait()
}

func applyDetail(sc *models.Scene, body string) {
	if m := metaRe.FindAllStringSubmatch(body, -1); len(m) >= 2 {
		if sc.Duration == 0 {
			sc.Duration = parseutil.ParseDurationColon(strings.TrimSpace(m[0][1]))
		}
		if sc.Date.IsZero() {
			if d, err := parseutil.TryParseDate(strings.TrimSpace(m[1][1]), dateLayout); err == nil {
				sc.Date = d
			}
		}
	}
	if d := descRe.FindStringSubmatch(body); d != nil {
		sc.Description = cleanText(d[1])
	}
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStrip.ReplaceAllString(s, " "))), " ")
}
