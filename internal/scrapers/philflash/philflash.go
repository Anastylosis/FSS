// Package philflash scrapes the phil-flash network: phil-flash.com itself, the
// single-model domains that are now landing pages for a portfolio or a fetish
// listing on it (teenkasia.com, meganqt.com, nextdoornikki.com, …), and
// karisweets.com, which runs the photographer's older engine separately.
//
// phil-flash.com publishes only its preview sets; the sitemap lists every one
// of them as /girls/{portfolio}/{set} or /events/{event}/{set}. Each set page
// carries the title, release date, runtime, tags and description. Photo-only
// sets are skipped.
package philflash

import (
	"context"
	"errors"
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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID      = "philflash"
	siteBase    = "https://phil-flash.com"
	networkName = "phil-flash"
	// ageCookie is what the site's own "I am 18 or older" button sets.
	ageCookie      = "phil_flash_adult_content_ack=1"
	defaultWorkers = 3
)

type targetKind int

const (
	targetAll targetKind = iota
	targetGirl
	targetEvent
	targetFetish
)

// childSite is a former single-model domain that now lands on a filtered view
// of phil-flash.com.
type childSite struct {
	host   string
	studio string
	kind   targetKind
	slug   string
}

var childSites = []childSite{
	{"brattybrittany.com", "Bratty Brittany", targetGirl, "bratty-brittany"},
	{"facedownassupuniversity.com", "Face Down Ass Up University", targetFetish, "schoolgirl-uniform"},
	{"karensdreamgirls.com", "Karen's Dream Girls", targetFetish, "photographed-by-karen-dreams"},
	{"meganqt.com", "MeganQt", targetGirl, "megan-qt"},
	{"nextdoornikki.com", "Next Door Nikki", targetGirl, "next-door-nikki"},
	{"taylortwins.com", "Taylor Twins", targetGirl, "taylor-twins"},
	{"teenkasia.com", "Teen Kasia", targetGirl, "teen-kasia"},
	{"tiffanyteen.com", "Tiffany Teen", targetGirl, "tiffany-teen"},
}

// portfolioStudio names the solo portfolios that had their own site, so a set
// keeps that studio whichever URL it was reached through.
var portfolioStudio = func() map[string]string {
	m := map[string]string{}
	for _, c := range childSites {
		if c.kind == targetGirl {
			m[c.slug] = c.studio
		}
	}
	return m
}()

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:phil-flash\.com|brattybrittany\.com|facedownassupuniversity\.com|karensdreamgirls\.com|meganqt\.com|nextdoornikki\.com|taylortwins\.com|teenkasia\.com|tiffanyteen\.com)(?:[/?#]|$)`)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
	scraper.Register(NewKariSweets())
}

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	p := []string{
		"phil-flash.com",
		"phil-flash.com/girls/{portfolio}",
		"phil-flash.com/events/{event}",
		"phil-flash.com/fetishes/{fetish}",
	}
	for _, c := range childSites {
		p = append(p, c.host)
	}
	return p
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type target struct {
	kind   targetKind
	slug   string
	studio string // non-empty when the run entered through a child domain
}

func resolveTarget(studioURL string) target {
	u, err := url.Parse(studioURL)
	if err != nil {
		return target{}
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	for _, c := range childSites {
		if host == c.host {
			return target{kind: c.kind, slug: c.slug, studio: c.studio}
		}
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[1] != "" {
		switch parts[0] {
		case "girls":
			return target{kind: targetGirl, slug: parts[1]}
		case "events":
			return target{kind: targetEvent, slug: parts[1]}
		case "fetishes":
			return target{kind: targetFetish, slug: parts[1]}
		}
	}
	return target{}
}

// setRef is one preview set: section is "girls" or "events".
type setRef struct {
	section   string
	portfolio string
	slug      string
	// exact is false for a set reached through a joint portfolio that merely
	// names the requested girl's slug; it is kept only if she is credited.
	exact bool
}

func (r setRef) id() string   { return r.portfolio + "/" + r.slug }
func (r setRef) path() string { return "/" + r.section + "/" + r.portfolio + "/" + r.slug }

var setPathRe = regexp.MustCompile(`(?:^|https?://[^/"<]+)/(girls|events)/([a-z0-9][^/"<?#]*)/([a-z0-9][^/"<?#]*)$`)

// parseSitemap returns every set page listed in the sitemap, skipping the
// /free-gallery children and the portfolio pages.
func parseSitemap(body []byte) []setRef {
	var refs []setRef
	seen := map[string]bool{}
	for _, m := range locRe.FindAllSubmatch(body, -1) {
		ref, ok := refFromPath(strings.TrimSpace(html.UnescapeString(string(m[1]))))
		if !ok || seen[ref.id()] {
			continue
		}
		seen[ref.id()] = true
		refs = append(refs, ref)
	}
	return refs
}

var locRe = regexp.MustCompile(`<loc>([^<]+)</loc>`)

func refFromPath(p string) (setRef, bool) {
	m := setPathRe.FindStringSubmatch(p)
	if m == nil {
		return setRef{}, false
	}
	return setRef{section: m[1], portfolio: m[2], slug: m[3], exact: true}, true
}

// girlRefs selects the sets of one girl: her own portfolio, plus joint
// portfolios whose slug contains hers as a hyphen-delimited run.
func girlRefs(all []setRef, girl string) []setRef {
	var refs []setRef
	for _, r := range all {
		if r.section != "girls" {
			continue
		}
		switch {
		case r.portfolio == girl:
			refs = append(refs, r)
		case strings.Contains("-"+r.portfolio+"-", "-"+girl+"-"):
			r.exact = false
			refs = append(refs, r)
		}
	}
	return refs
}

var (
	fetishSetRe       = regexp.MustCompile(`href="((?:https?://[^/"]+)?/(?:girls|events)/[a-z0-9][^/"?#]*/[a-z0-9][^/"?#]*)"`)
	fetishPortfolioRe = regexp.MustCompile(`href="(?:https?://[^/"]+)?/girls/([a-z0-9][^/"?#]*)"`)
)

// fetishRefs reads a /fetishes/{slug} page. It links either sets directly or,
// for a photographer listing like Karen's Dream Girls, whole portfolios, whose
// sets come from the sitemap.
func fetishRefs(page []byte, all []setRef) []setRef {
	var refs []setRef
	seen := map[string]bool{}
	add := func(r setRef) {
		if !seen[r.id()] {
			seen[r.id()] = true
			refs = append(refs, r)
		}
	}
	for _, m := range fetishSetRe.FindAllSubmatch(page, -1) {
		if r, ok := refFromPath(string(m[1])); ok {
			add(r)
		}
	}
	portfolios := map[string]bool{}
	for _, m := range fetishPortfolioRe.FindAllSubmatch(page, -1) {
		portfolios[string(m[1])] = true
	}
	for _, r := range all {
		if r.section == "girls" && portfolios[r.portfolio] {
			add(r)
		}
	}
	return refs
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	send := func(r scraper.SceneResult) bool {
		select {
		case out <- r:
			return true
		case <-ctx.Done():
			return false
		}
	}

	tgt := resolveTarget(studioURL)
	scraper.Debugf(1, "philflash: target kind=%d slug=%q", tgt.kind, tgt.slug)

	sitemapURL := s.base + "/sitemap.xml"
	body, err := s.fetch(ctx, sitemapURL, opts.Cookie)
	if err != nil {
		send(scraper.Error(fmt.Errorf("sitemap: %w", err)))
		return
	}
	all := parseSitemap(body)
	if len(all) == 0 {
		send(scraper.Error(scraper.ParseError(sitemapURL, fmt.Errorf("no set pages in sitemap"))))
		return
	}

	var refs []setRef
	switch tgt.kind {
	case targetGirl:
		refs = girlRefs(all, tgt.slug)
	case targetEvent:
		for _, r := range all {
			if r.section == "events" && r.portfolio == tgt.slug {
				refs = append(refs, r)
			}
		}
	case targetFetish:
		if !scraper.Pace(ctx, opts.Delay) {
			return
		}
		fetishURL := s.base + "/fetishes/" + tgt.slug
		page, err := s.fetch(ctx, fetishURL, opts.Cookie)
		if err != nil {
			send(scraper.Error(fmt.Errorf("fetish %s: %w", tgt.slug, err)))
			return
		}
		refs = fetishRefs(page, all)
	default:
		refs = all
	}
	scraper.Debugf(1, "philflash: %d candidate sets", len(refs))

	var todo []setRef
	skipped := 0
	for _, r := range refs {
		if opts.KnownIDs[r.id()] {
			skipped++
			continue
		}
		todo = append(todo, r)
	}
	if !send(scraper.Progress(len(refs))) {
		return
	}

	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "philflash: fetching %d set pages with %d workers (%d known)", len(todo), workers, skipped)
	work := make(chan setRef)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, ok, err := s.fetchSet(ctx, r, tgt, studioURL, opts.Cookie)
				if err != nil {
					if !send(scraper.Error(err)) {
						return
					}
					continue
				}
				if ok && !send(scraper.Scene(scene)) {
					return
				}
			}
		}()
	}
	go func() {
		defer close(work)
		for _, r := range todo {
			select {
			case work <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()

	if skipped > 0 && ctx.Err() == nil {
		send(scraper.StoppedEarly())
	}
}

func (s *Scraper) fetchSet(ctx context.Context, r setRef, tgt target, studioURL, cookie string) (models.Scene, bool, error) {
	pageURL := s.base + r.path()
	body, err := s.fetch(ctx, pageURL, cookie)
	if err != nil {
		// The sitemap lists some sets whose pages are gone; such a set was
		// never part of the public catalogue, so it must not mark the run
		// incomplete.
		var se *httpx.StatusError
		if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
			return models.Scene{}, false, scraper.AbsentError(pageURL, err)
		}
		return models.Scene{}, false, fmt.Errorf("set %s: %w", r.id(), err)
	}
	d, err := parseSet(body, time.Now())
	if err != nil {
		return models.Scene{}, false, scraper.ParseError(pageURL, err)
	}
	if d.title == "" {
		// A handful of legacy sets carry no title anywhere on the page.
		d.title = slugTitle(r.slug)
	}
	if d.videos == 0 && d.duration == 0 {
		scraper.Debugf(1, "philflash: %s is a photo set, skipped", r.id())
		return models.Scene{}, false, nil
	}
	performers := d.portfolioCast
	if r.section == "events" || len(performers) == 0 {
		performers = d.titleCast
	}
	if !r.exact && !credits(performers, tgt.slug) {
		return models.Scene{}, false, nil
	}

	studio := tgt.studio
	if studio == "" {
		studio = portfolioStudio[r.portfolio]
	}
	if studio == "" {
		studio = networkName
	}

	scene := models.Scene{
		ID:          r.id(),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       d.title,
		URL:         siteBase + r.path(),
		Date:        d.date,
		Description: d.description,
		Thumbnail:   d.thumbnail,
		Preview:     d.preview,
		Performers:  performers,
		Studio:      studio,
		Tags:        d.tags,
		Duration:    d.duration,
		ScrapedAt:   time.Now().UTC(),
	}
	if r.section == "events" {
		scene.Series = d.portfolioName
	}
	return scene, true, nil
}

// credits reports whether a girl's slug names one of the performers, comparing
// letters and digits only ("megan-qt" is "MeganQt").
func credits(performers []string, slug string) bool {
	want := alnum(slug)
	for _, p := range performers {
		if alnum(p) == want {
			return true
		}
	}
	return false
}

func slugTitle(slug string) string {
	words := strings.Fields(strings.ReplaceAll(slug, "-", " "))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func alnum(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type setData struct {
	title         string
	date          time.Time
	description   string
	thumbnail     string
	preview       string
	portfolioName string
	portfolioCast []string
	titleCast     []string
	tags          []string
	videos        int
	duration      int
}

var (
	titleTagRe    = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	metaDescRe    = regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
	dataCenterRe  = regexp.MustCompile(`<h2><span>([^<]+)</span>\s*<span>Data Center</span></h2>`)
	crumbRe       = regexp.MustCompile(`<li><a href="/(girls|events)/[^"/]+" class="chip-style[^"]*">\s*([^<]+?)\s*</a></li>`)
	timeRe        = regexp.MustCompile(`<time datetime='([^']+)'`)
	tableRowRe    = regexp.MustCompile(`(?s)<div class='table__rows__row'><div class='table__rows__row__col'>(.*?)</div><div class='table__rows__row__col'>([^<]*)</div></div>`)
	tagRe         = regexp.MustCompile(`<a href="/fetishes/[^"]+"><h3>([^<]+)</h3></a>`)
	posterAttrRe  = regexp.MustCompile(`<video[^>]*\sposter="([^"]+)"`)
	thumbSetRe    = regexp.MustCompile(`<img class="thumbnail-set" src="([^"]+)"`)
	posterBlockRe = regexp.MustCompile(`(?s)id='poster-mobile'>(.*?)</div>`)
	imgSrcRe      = regexp.MustCompile(`<img[^>]*\ssrc="([^"]+)"`)
	previewSrcRe  = regexp.MustCompile(`<source src="([^"]+\.mp4)" type="video/mp4"`)
	tagStripRe    = regexp.MustCompile(`<[^>]+>`)
	castSplitRe   = regexp.MustCompile(`(?i)\s*,\s*(?:and\s+)?|\s+and\s+|\s*&\s*`)
	setTagsAreaRe = regexp.MustCompile(`(?s)id="set-tags">(.*?)</ul>`)
)

// parseSet reads a set page. A set with no stored release date is rendered
// with the server's current time, so a date within a day of now is dropped.
func parseSet(body []byte, now time.Time) (setData, error) {
	page := string(body)
	var d setData

	var pageTitle string
	if m := titleTagRe.FindStringSubmatch(page); m != nil {
		pageTitle = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := dataCenterRe.FindStringSubmatch(page); m != nil {
		d.title = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if i := strings.LastIndex(pageTitle, " | "); i >= 0 {
		if d.title == "" {
			d.title = strings.TrimSpace(pageTitle[:i])
		}
		d.titleCast = splitCast(pageTitle[i+3:])
	}
	if !strings.Contains(page, "Data Center</span></h2>") {
		return d, fmt.Errorf("no set data block")
	}

	if m := crumbRe.FindStringSubmatch(page); m != nil {
		d.portfolioName = html.UnescapeString(m[2])
		if m[1] == "girls" {
			d.portfolioCast = splitCast(d.portfolioName)
		}
	}
	if m := timeRe.FindStringSubmatch(page); m != nil {
		if t, err := parseutil.TryParseDate(strings.TrimSpace(m[1]), "2006-01-02T15:04:05", "2006-01-02"); err == nil {
			if off := t.Sub(now); off < -24*time.Hour || off > 24*time.Hour {
				d.date = t.UTC()
			}
		}
	}
	if m := metaDescRe.FindStringSubmatch(page); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	for _, m := range tableRowRe.FindAllStringSubmatch(page, -1) {
		label := strings.ToLower(strings.TrimSpace(html.UnescapeString(tagStripRe.ReplaceAllString(m[1], ""))))
		val := strings.TrimSpace(m[2])
		switch label {
		case "videos":
			_, _ = fmt.Sscanf(val, "%d", &d.videos)
		case "total runtime":
			d.duration = parseutil.ParseDurationColon(val)
		}
	}
	if area := setTagsAreaRe.FindStringSubmatch(page); area != nil {
		seen := map[string]bool{}
		for _, m := range tagRe.FindAllStringSubmatch(area[1], -1) {
			t := strings.TrimSpace(html.UnescapeString(m[1]))
			if t != "" && !seen[t] {
				seen[t] = true
				d.tags = append(d.tags, t)
			}
		}
	}
	// The hero poster is a trailer <video> on video sets and an <img> on the
	// rest; the footer's site-wide "about" video must not be mistaken for it.
	hero := ""
	if m := posterBlockRe.FindStringSubmatch(page); m != nil {
		hero = m[1]
	}
	if m := posterAttrRe.FindStringSubmatch(hero); m != nil {
		d.thumbnail = absURL(m[1])
	} else if m := imgSrcRe.FindStringSubmatch(hero); m != nil {
		d.thumbnail = absURL(m[1])
	} else if m := thumbSetRe.FindStringSubmatch(page); m != nil {
		d.thumbnail = absURL(m[1])
	}
	if m := previewSrcRe.FindStringSubmatch(hero); m != nil {
		d.preview = absURL(m[1])
	}
	return d, nil
}

func splitCast(s string) []string {
	var out []string
	for _, p := range castSplitRe.Split(strings.TrimSpace(html.UnescapeString(s)), -1) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func absURL(u string) string {
	if strings.HasPrefix(u, "/") {
		return siteBase + u
	}
	return u
}

func (s *Scraper) fetch(ctx context.Context, u, cookie string) ([]byte, error) {
	return fetchPage(ctx, s.client, u, cookie)
}

func fetchPage(ctx context.Context, client *http.Client, u, cookie string) ([]byte, error) {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	c := ageCookie
	if cookie != "" {
		c += "; " + cookie
	}
	h["Cookie"] = c
	resp, err := httpx.Do(ctx, client, httpx.Request{URL: u, Headers: h})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
