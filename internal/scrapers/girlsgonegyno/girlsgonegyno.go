// Package girlsgonegyno scrapes the PornCMS.com (Doctor Tampa / BuyMyMovies)
// sites served via privatemediacloud.com: GirlsGoneGyno and CaptiveClinic.
//
// The video grid is lazy-loaded: the "Videos" listing page
// (?mb=VmlkZW9zfHw=, base64 for "Videos||") is a thin wrapper whose
// #mainbody is populated by a jQuery .load() call pointing at a per-page
// content fragment under content/pages/<hash>.list.htm. Pagination is
// driven by index.php?mb=VmlkZW9zfHw=&clearcache=1&p=<offset> where offset
// steps by 27 (the page size); each wrapper embeds the fragment hash for
// that page. So each page is two requests: fetch the wrapper to discover
// the fragment hash, then fetch the fragment grid and parse the cards.
//
// Each card carries the full per-scene metadata (video id, title,
// performers, posted date, length, views, thumbnail), so no detail-page
// fetch is needed. The scene detail/trailer URL is ?mb=Trailer||<id>
// (base64-encoded).
package girlsgonegyno

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const pageSize = 27

// videosMB is base64 for "Videos||", the PornCMS menu token for the
// newest-first video listing.
const videosMB = "VmlkZW9zfHw="

// SiteConfig describes one PornCMS site (Doctor Tampa / BuyMyMovies network).
type SiteConfig struct {
	ID       string
	SiteBase string // e.g. "https://www.girlsgonegyno.com" — no trailing slash
	Studio   string
	MatchRe  *regexp.Regexp
	// PageSize is the listing's cards per page (default 27).
	PageSize int
	// Performer credits every scene to one person on a site whose cards
	// carry no Model line (a performer's own store).
	Performer string
	// InlineCards means the listing page carries its cards directly rather
	// than lazy-loading them from a content/pages fragment (the Bootstrap 4
	// card template, e.g. Diapered Online).
	InlineCards bool
}

var sites = []SiteConfig{
	{
		ID:       "girlsgonegyno",
		SiteBase: "https://www.girlsgonegyno.com",
		Studio:   "GirlsGoneGyno",
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?girlsgonegyno\.com(?:/|$)`),
	},
	{
		ID:       "captiveclinic",
		SiteBase: "https://www.captiveclinic.com",
		Studio:   "CaptiveClinic",
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?captiveclinic\.com(?:/|$)`),
	},
	{
		ID:        "lelulove",
		SiteBase:  "https://www.lelulove.com",
		Studio:    "Lelu Love",
		MatchRe:   regexp.MustCompile(`^https?://(?:www\.)?lelulove\.com(?:/|$)`),
		PageSize:  9,
		Performer: "Lelu Love",
	},
	{
		ID:          "diaperedonline",
		SiteBase:    "https://www.diaperedonline.com",
		Studio:      "Diapered Online",
		MatchRe:     regexp.MustCompile(`^https?://(?:www\.)?diaperedonline\.com(?:/|$)`),
		PageSize:    15,
		InlineCards: true,
	},
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	if cfg.PageSize == 0 {
		cfg.PageSize = pageSize
	}
	return &Scraper{cfg: cfg, client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.ID }

func (s *Scraper) Patterns() []string {
	return []string{strings.TrimPrefix(s.cfg.SiteBase, "https://www.")}
}

func (s *Scraper) MatchesURL(u string) bool { return s.cfg.MatchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		offset := (page - 1) * s.cfg.PageSize
		cards, err := s.fetchPage(ctx, offset)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, 0, len(cards))
		for _, c := range cards {
			if sc, ok := s.toScene(studioURL, c, now); ok {
				scenes = append(scenes, sc)
			}
		}
		return scraper.PageResult{Scenes: scenes, Done: len(cards) < s.cfg.PageSize}, nil
	})
}

var (
	loadHashRe        = regexp.MustCompile(`\.load\("(content/pages/[a-z0-9_]+\.list\.htm)"\)`)
	cardSplitRe       = regexp.MustCompile(`<div class='col-sm-4 img-portfolio'>`)
	inlineCardSplitRe = regexp.MustCompile(`<div class="col-12 col-sm-6 col-md-4 pl-2 pr-2 pb-4">`)
)

// fetchPage fetches the wrapper for the given offset, extracts the lazy-load
// fragment URL, fetches that fragment, and returns the raw per-card HTML
// chunks.
func (s *Scraper) fetchPage(ctx context.Context, offset int) ([]string, error) {
	wrapperURL := fmt.Sprintf("%s/index.php?mb=%s&clearcache=1&p=%d", s.cfg.SiteBase, videosMB, offset)
	wrapper, err := s.fetchBody(ctx, wrapperURL)
	if err != nil {
		return nil, fmt.Errorf("fetching wrapper: %w", err)
	}
	if s.cfg.InlineCards {
		return splitCards(wrapper, inlineCardSplitRe), nil
	}
	m := loadHashRe.FindStringSubmatch(wrapper)
	if m == nil {
		scraper.Debugf(1, "%s: no fragment hash in wrapper for offset %d", s.cfg.ID, offset)
		return nil, nil
	}
	fragURL := s.cfg.SiteBase + "/" + m[1]
	scraper.Debugf(1, "%s: loading fragment %s", s.cfg.ID, m[1])
	frag, err := s.fetchBody(ctx, fragURL)
	if err != nil {
		return nil, fmt.Errorf("fetching fragment: %w", err)
	}
	return splitCards(frag, cardSplitRe), nil
}

// splitCards returns the chunks after the first split marker, one per card.
func splitCards(html string, marker *regexp.Regexp) []string {
	parts := marker.Split(html, -1)
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}

func (s *Scraper) fetchBody(ctx context.Context, u string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
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

var (
	ridRe        = regexp.MustCompile(`data-rid="([a-z0-9]+)"`)
	trailerRe    = regexp.MustCompile(`\?mb=([A-Za-z0-9+/=]+)`)
	titleRe      = regexp.MustCompile(`(?s)<h[45][^>]*>\s*<a[^>]+>([^<]+)</a>`)
	thumbRe      = regexp.MustCompile(`<img class=["'][^"']*thumbvideo[^"']*["'][^>]*src=["']([^"']+)["']`)
	modelBlockRe = regexp.MustCompile(`(?s)<strong>Model:\s*</strong>(.*?)(?:<br>|</p>)`)
	performerRe  = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
	postedRe     = regexp.MustCompile(`<strong>Posted:\s*</strong>\s*([^<]+)`)
	lengthRe     = regexp.MustCompile(`<strong>Length:\s*</strong>([0-9:]+)`)
	viewsRe      = regexp.MustCompile(`<strong>Views:\s*</strong>([0-9,]+)`)
)

func (s *Scraper) toScene(studioURL, card string, now time.Time) (models.Scene, bool) {
	id := cardID(card)
	if id == "" {
		return models.Scene{}, false
	}
	scene := models.Scene{
		ID:        id,
		SiteID:    s.cfg.ID,
		StudioURL: studioURL,
		URL:       s.cfg.SiteBase + "/?mb=" + base64.StdEncoding.EncodeToString([]byte("Trailer||"+id)),
		Studio:    s.cfg.Studio,
		ScrapedAt: now,
	}
	if t := titleRe.FindStringSubmatch(card); t != nil {
		scene.Title = html.UnescapeString(strings.TrimSpace(t[1]))
	}
	if th := thumbRe.FindStringSubmatch(card); th != nil {
		scene.Thumbnail = th[1]
	}
	if s.cfg.Performer != "" {
		scene.Performers = []string{s.cfg.Performer}
	}
	if mb := modelBlockRe.FindStringSubmatch(card); mb != nil {
		for _, p := range performerRe.FindAllStringSubmatch(mb[1], -1) {
			name := html.UnescapeString(strings.TrimSpace(p[1]))
			if name != "" {
				scene.Performers = append(scene.Performers, name)
			}
		}
	}
	if p := postedRe.FindStringSubmatch(card); p != nil {
		if d, err := parseutil.TryParseDate(strings.TrimSpace(p[1]), "Mon, 2 Jan 2006"); err == nil {
			scene.Date = d.UTC()
		}
	}
	if l := lengthRe.FindStringSubmatch(card); l != nil {
		scene.Duration = parseutil.ParseDurationColon(l[1])
	}
	if v := viewsRe.FindStringSubmatch(card); v != nil {
		if n, err := strconv.Atoi(strings.ReplaceAll(v[1], ",", "")); err == nil {
			scene.Views = n
		}
	}
	return scene, true
}

// cardID is the card's data-rid, or on sites without one the id inside the
// base64 "Trailer||<id>" link.
func cardID(card string) string {
	if m := ridRe.FindStringSubmatch(card); m != nil {
		return m[1]
	}
	for _, m := range trailerRe.FindAllStringSubmatch(card, -1) {
		if b, err := base64.StdEncoding.DecodeString(m[1]); err == nil {
			if id, ok := strings.CutPrefix(string(b), "Trailer||"); ok && id != "" {
				return id
			}
		}
	}
	return ""
}
