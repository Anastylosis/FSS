// Package kissmegirl scrapes the GreyWood network tours (KissMe Girl,
// KissMe Girl Explicit, Sloppy Girl). Each is a static HTML site whose scene
// listing is a series of hand-numbered pages (vod.html, vodpage2.html, …),
// one <table> per scene. The tours publish no dates.
package kissmegirl

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one tour of the network.
type SiteConfig struct {
	ID          string
	Base        string // scheme+host, no trailing slash
	Studio      string
	FirstPage   string // listing page 1, relative to Base
	PagePattern string // listing page N (N ≥ 2), fmt pattern relative to Base
	ModelPages  bool   // serves /models/{slug}/{slug}.html
	MatchRe     *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func New(cfg SiteConfig) *Scraper {
	return &Scraper{cfg: cfg, Client: httpx.NewClient(30 * time.Second)}
}

func (s *Scraper) ID() string { return s.cfg.ID }

func (s *Scraper) Patterns() []string {
	host := strings.TrimPrefix(strings.TrimPrefix(s.cfg.Base, "https://"), "www.")
	p := []string{host + "/"}
	if s.cfg.ModelPages {
		p = append(p, host+"/models/{slug}/{slug}.html")
	}
	return p
}

func (s *Scraper) MatchesURL(u string) bool { return s.cfg.MatchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var modelPathRe = regexp.MustCompile(`^/models/([\w-]+)/([\w-]+)\.html$`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	// The listing is not in release order (the front page and vod.html
	// disagree on which scene is newest) and carries no dates, so a stored
	// scene says nothing about what follows it. The catalogue is ~13 pages:
	// walk it all.
	opts.KnownIDs = nil
	now := time.Now().UTC()

	if s.cfg.ModelPages {
		if u, err := url.Parse(studioURL); err == nil && modelPathRe.MatchString(u.Path) {
			scraper.Debugf(1, "%s: scraping model page %s", s.cfg.ID, u.Path)
			pageURL := s.cfg.Base + u.Path
			scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, _ int) (scraper.PageResult, error) {
				scenes, _, err := s.fetchPage(ctx, pageURL, studioURL, 0, now)
				return scraper.PageResult{Scenes: scenes, Done: true}, err
			})
			return
		}
	}

	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		scenes, hasNext, err := s.fetchPage(ctx, s.pageURL(page), studioURL, page, now)
		if err != nil {
			return scraper.PageResult{}, err
		}
		return scraper.PageResult{Scenes: scenes, Done: !hasNext}, nil
	})
}

func (s *Scraper) pageURL(page int) string {
	if page <= 1 {
		return s.cfg.Base + "/" + s.cfg.FirstPage
	}
	return s.cfg.Base + "/" + fmt.Sprintf(s.cfg.PagePattern, page)
}

// fetchPage fetches one listing page and reports whether it links to page+1.
// The pages are hand-edited: the "next" link's case varies, and some pages
// also link to themselves, so only a link to exactly page+1 counts.
func (s *Scraper) fetchPage(ctx context.Context, pageURL, studioURL string, page int, now time.Time) ([]models.Scene, bool, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", pageURL, err)
	}

	scenes := s.parsePage(string(body), pageURL, studioURL, now)
	if len(scenes) == 0 {
		return nil, false, scraper.ParseError(pageURL, errors.New("no scene tables found"))
	}
	hasNext := false
	if page > 0 {
		next := strings.ToLower(path.Base(s.pageURL(page + 1)))
		hasNext = strings.Contains(strings.ToLower(string(body)), `href="`+next+`"`)
	}
	return scenes, hasNext, nil
}

// ---- parsing ----

var (
	tableSplitRe = regexp.MustCompile(`(?i)<table[\s>]`)
	titleRe      = regexp.MustCompile(`(?is)colspan="3"><b>(.*?)</b>`)
	thumbRe      = regexp.MustCompile(`(?i)<img src="([^"]*?([\w-]+)small\.jpg)"`)
	sceneLinkRe  = regexp.MustCompile(`(?i)href="([^"]*scenepages/[\w-]+\.html)"`)
	lengthRe     = regexp.MustCompile(`(?i)LENGTH:\s*</b>\s*(\d+)\s*MIN`)
	starsRe      = regexp.MustCompile(`(?is)Stars:\s*</b>(.*?)</td>`)
	resRe        = regexp.MustCompile(`(?i)(?:HD|RESOLUTION):\s*</b>\s*(\d+)\s*x\s*(\d+)`)
	formatRe     = regexp.MustCompile(`(?i)(?:Media|FILE TYPE):\s*</b>([^<]+)`)
	descRe       = regexp.MustCompile(`(?is)Desc:\s*</b>.*?<P>(.*?)</td>`)
	priceRe      = regexp.MustCompile(`<b>\$(\d+(?:\.\d+)?)</b>`)
	tagRe        = regexp.MustCompile(`<[^>]*>`)
	spaceRe      = regexp.MustCompile(`\s+`)
	starSplitRe  = regexp.MustCompile(`\s*(?:,|&amp;|&)\s*`)
	titlePrefixR = regexp.MustCompile(`^.*?\s+-\s+`)
)

func (s *Scraper) parsePage(body, pageURL, studioURL string, now time.Time) []models.Scene {
	base, _ := url.Parse(pageURL)
	var scenes []models.Scene
	locs := tableSplitRe.FindAllStringIndex(body, -1)
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]
		if !lengthRe.MatchString(block) {
			continue
		}
		if sc, ok := s.parseBlock(block, base, studioURL, now); ok {
			scenes = append(scenes, sc)
		}
	}
	return scenes
}

func (s *Scraper) parseBlock(block string, base *url.URL, studioURL string, now time.Time) (models.Scene, bool) {
	sc := models.Scene{
		SiteID:    s.cfg.ID,
		StudioURL: studioURL,
		Studio:    s.cfg.Studio,
		ScrapedAt: now,
	}
	if m := thumbRe.FindStringSubmatch(block); m != nil {
		sc.ID = strings.ToLower(m[2])
		sc.Thumbnail = resolve(base, m[1])
	}
	if m := sceneLinkRe.FindStringSubmatch(block); m != nil {
		sc.URL = resolve(base, m[1])
	}
	if m := starsRe.FindStringSubmatch(block); m != nil {
		for _, p := range starSplitRe.Split(cleanText(m[1]), -1) {
			if p = strings.TrimSpace(p); p != "" {
				sc.Performers = append(sc.Performers, p)
			}
		}
	}
	if m := titleRe.FindStringSubmatch(block); m != nil {
		sc.Title = prettyTitle(cleanText(m[1]), sc.Performers)
	}
	if sc.ID == "" || sc.Title == "" {
		return sc, false
	}
	if sc.URL == "" {
		u := *base
		u.Fragment = sc.ID
		sc.URL = u.String()
	}
	if m := lengthRe.FindStringSubmatch(block); m != nil {
		mins, _ := strconv.Atoi(m[1])
		sc.Duration = mins * 60
	}
	if m := resRe.FindStringSubmatch(block); m != nil {
		sc.Width, _ = strconv.Atoi(m[1])
		sc.Height, _ = strconv.Atoi(m[2])
		sc.Resolution = m[1] + "x" + m[2]
	}
	if m := formatRe.FindStringSubmatch(block); m != nil {
		sc.Format = strings.TrimSpace(m[1])
	}
	if m := descRe.FindStringSubmatch(block); m != nil {
		d := cleanText(m[1])
		d = strings.TrimSpace(strings.ReplaceAll(d, "Watch Trailer!!", ""))
		sc.Description = spaceRe.ReplaceAllString(d, " ")
	}
	if m := priceRe.FindStringSubmatch(block); m != nil {
		if p, err := strconv.ParseFloat(m[1], 64); err == nil && p > 0 {
			sc.AddPrice(models.PriceSnapshot{Date: now, Regular: p})
		}
	}
	return sc, true
}

func resolve(base *url.URL, ref string) string {
	r, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return ""
	}
	return base.ResolveReference(r).String()
}

func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

// prettyTitle turns "KISSME GIRL - AJ & MISCHA" into "AJ & Mischa": the
// site-name prefix goes, and words take the performer credit's spelling when
// one matches, title case otherwise.
func prettyTitle(raw string, performers []string) string {
	t := titlePrefixR.ReplaceAllString(raw, "")
	if t == "" {
		t = raw
	}
	spelled := map[string]string{}
	for _, p := range performers {
		for _, w := range strings.Fields(p) {
			spelled[strings.ToLower(w)] = w
		}
	}
	words := strings.Fields(t)
	for i, w := range words {
		lw := strings.ToLower(w)
		if sp, ok := spelled[lw]; ok {
			words[i] = sp
		} else if w == strings.ToUpper(w) {
			words[i] = strings.ToUpper(lw[:1]) + lw[1:]
		}
	}
	return strings.Join(words, " ")
}
