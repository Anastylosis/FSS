// Package mooduniverse scrapes the Mood Universe download shops (ElitePain,
// Mood Pictures, Cruel Mistresses), which share one PHP shop whose movies.php
// lists the whole catalogue on a single page. See docs/scrapers.md.
package mooduniverse

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig is one shop of the network.
type SiteConfig struct {
	SiteID     string
	StudioName string
	Domain     string
}

var sites = []SiteConfig{
	{SiteID: "elitepain", StudioName: "ElitePain", Domain: "elitepain.com"},
	{SiteID: "moodpictures", StudioName: "Mood Pictures", Domain: "mood-pictures.com"},
	{SiteID: "cruelmistresses", StudioName: "Cruel Mistresses", Domain: "cruel-mistresses.com"},
}

// testBase redirects every shop to a test server.
var testBase string

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

var (
	cardSplit = regexp.MustCompile(`<div id="[^"]*" class="movie `)
	idRe      = regexp.MustCompile(`<a href="movie\.php\?id=(\d+)`)
	imgRe     = regexp.MustCompile(`<a href="movie\.php[^"]*"><img src="([^"]+)"`)
	titleRe   = regexp.MustCompile(`<h3>([^<]*)</h3>`)
	strikeRe  = regexp.MustCompile(`<strike><strong>\s*([\d.]+)\s*</strong></strike>`)
	priceRe   = regexp.MustCompile(`<strong[^>]*>\s*([\d.]+)\s*EUR\s*</strong>`)
	castRe    = regexp.MustCompile(`<span>(?:Mistress(?:es)?|Models?|Dominas?):\s*</span>\s*([^<]*)`)
	detailsRe = regexp.MustCompile(`<span>Details:\s*</span>\s*([^<]*)`)
	hoursRe   = regexp.MustCompile(`(\d+)\s*hours?`)
	minsRe    = regexp.MustCompile(`(\d+)\s*min`)
	splitRe   = regexp.MustCompile(`\s*(?:,|&|\band\b)\s*`)
)

type Scraper struct {
	cfg    SiteConfig
	match  *regexp.Regexp
	Client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{cfg: cfg, match: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`), Client: httpx.NewClient(60 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{s.cfg.Domain, s.cfg.Domain + "/movies.php"}
}

func (s *Scraper) MatchesURL(u string) bool {
	return s.match.MatchString(u)
}

func (s *Scraper) base() string {
	if testBase != "" {
		return testBase
	}
	return "https://" + s.cfg.Domain
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, out chan<- scraper.SceneResult) {
	defer close(out)
	send := func(r scraper.SceneResult) bool {
		select {
		case out <- r:
			return true
		case <-ctx.Done():
			return false
		}
	}
	u := s.base() + "/movies.php"
	scraper.Debugf(1, "%s: fetching %s", s.cfg.SiteID, u)
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		send(scraper.Error(err))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := httpx.ReadBody(resp.Body)
	if err != nil {
		send(scraper.Error(err))
		return
	}
	scenes := s.parse(string(b), studioURL, time.Now().UTC())
	if len(scenes) == 0 {
		send(scraper.Error(scraper.ParseError(u, fmt.Errorf("no movies listed"))))
		return
	}
	if !send(scraper.Progress(len(scenes))) {
		return
	}
	for _, sc := range scenes {
		if !send(scraper.Scene(sc)) {
			return
		}
	}
}

func (s *Scraper) parse(body, studioURL string, now time.Time) []models.Scene {
	var scenes []models.Scene
	seen := map[string]bool{}
	for _, card := range cardSplit.Split(body, -1)[1:] {
		id := idRe.FindStringSubmatch(card)
		title := titleRe.FindStringSubmatch(card)
		if id == nil || title == nil || seen[id[1]] {
			continue
		}
		seen[id[1]] = true
		sc := models.Scene{
			ID:        id[1],
			SiteID:    s.cfg.SiteID,
			StudioURL: studioURL,
			Title:     clean(title[1]),
			URL:       s.base() + "/movie.php?id=" + id[1],
			Studio:    s.cfg.StudioName,
			ScrapedAt: now,
		}
		if m := imgRe.FindStringSubmatch(card); m != nil {
			sc.Thumbnail = s.base() + "/" + strings.TrimPrefix(html.UnescapeString(m[1]), "/")
		}
		if m := castRe.FindStringSubmatch(card); m != nil {
			for _, name := range splitRe.Split(clean(m[1]), -1) {
				if name != "" && name != "-" {
					sc.Performers = append(sc.Performers, name)
				}
			}
		}
		if m := detailsRe.FindStringSubmatch(card); m != nil {
			sc.Duration, sc.Description = parseDetails(clean(m[1]))
		}
		addPrice(&sc, card, now)
		scenes = append(scenes, sc)
	}
	return scenes
}

func addPrice(sc *models.Scene, card string, now time.Time) {
	m := priceRe.FindStringSubmatch(card)
	if m == nil {
		return
	}
	cur, err := strconv.ParseFloat(m[1], 64)
	if err != nil || cur <= 0 {
		return
	}
	snap := models.PriceSnapshot{Date: now, Regular: cur}
	if st := strikeRe.FindStringSubmatch(card); st != nil {
		if reg, err := strconv.ParseFloat(st[1], 64); err == nil && reg > cur {
			snap = models.PriceSnapshot{Date: now, Regular: reg, Discounted: cur, IsOnSale: true,
				DiscountPercent: int((reg-cur)/reg*100 + 0.5)}
		}
	}
	sc.AddPrice(snap)
}

// parseDetails reads "1 hour 15 min.; single tail whips, strap": the runtime
// and what follows it.
func parseDetails(s string) (duration int, rest string) {
	head, tail := s, ""
	if i := strings.IndexAny(s, ";,"); i >= 0 {
		head, tail = s[:i], strings.TrimSpace(s[i+1:])
	}
	if m := hoursRe.FindStringSubmatch(head); m != nil {
		n, _ := strconv.Atoi(m[1])
		duration += n * 3600
	}
	if m := minsRe.FindStringSubmatch(head); m != nil {
		n, _ := strconv.Atoi(m[1])
		duration += n * 60
	}
	if duration == 0 {
		return 0, s
	}
	return duration, tail
}

func clean(s string) string { return strings.Join(strings.Fields(html.UnescapeString(s)), " ") }
