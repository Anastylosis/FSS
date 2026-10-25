// Package indianaram scrapes the four Indiana RAM Productions sites
// (Slow Teasing Hand Jobs, Reluctant Young Men, Straight Men In Trouble,
// Hard Up Straight Guys). They are hand-built static pages: one updates page
// plus numbered archive pages, every scene anchored by a link into the
// members area.
package indianaram

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type siteConfig struct {
	id     string
	studio string
	host   string
	prefix string
	// models is true for the one site with a model directory.
	models bool
}

var sites = []siteConfig{
	{id: "slowteasinghandjobs", studio: "Slow Teasing Hand Jobs", host: "slowteasinghandjobs.com", prefix: "sthj", models: true},
	{id: "reluctantyoungmen", studio: "ReluctantYoungMen", host: "reluctantyoungmen.com", prefix: "rym"},
	{id: "straightmenintrouble", studio: "Straight Men In Trouble", host: "straightmenintrouble.com", prefix: "smit"},
	{id: "hardupstraightguys", studio: "HardUpStraightGuys", host: "hardupstraightguys.com", prefix: "husg"},
}

type Scraper struct {
	Client *http.Client
	cfg    siteConfig
	match  *regexp.Regexp
	// base overrides the site root in tests.
	base string
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func newScraper(cfg siteConfig) *Scraper {
	return &Scraper{
		Client: httpx.NewClient(45 * time.Second),
		cfg:    cfg,
		match:  regexp.MustCompile(`(?i)^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.host) + `(?:[/?#]|$)`),
		base:   "https://" + cfg.host,
	}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newScraper(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.id }

func (s *Scraper) Patterns() []string {
	p := []string{s.cfg.host, s.cfg.host + "/{page}"}
	if s.cfg.models {
		p = append(p, s.cfg.host+"/"+s.cfg.prefix+"_directory/model_pages/{model}")
	}
	return p
}

func (s *Scraper) MatchesURL(u string) bool {
	return s.match.MatchString(u)
}

var modelPathRe = regexp.MustCompile(`(?i)/[a-z]+_directory/model_pages/[^/]+\.html?$`)

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	if u, err := url.Parse(studioURL); err == nil && s.cfg.models && modelPathRe.MatchString(u.Path) {
		scraper.Debugf(1, "%s: scraping model page", s.cfg.id)
		s.runModel(ctx, studioURL, u.Path, opts, now, out)
		return
	}

	seen := map[string]string{}
	visited := map[int]bool{}
	archives := map[int]string{}
	scraper.Paginate(ctx, opts, s.cfg.id, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + "/" + s.cfg.prefix + "_updates.htm"
		if page > 1 {
			n, ok := nextArchive(archives, visited)
			if !ok {
				return scraper.PageResult{Done: true}, nil
			}
			visited[n] = true
			pageURL = s.base + "/" + archives[n]
		}
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		for n, p := range archiveLinks(body, s.cfg.prefix) {
			archives[n] = p
		}
		entries := parseListing(body, pageURL)
		if len(entries) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scenes found"))
		}
		scenes := make([]models.Scene, 0, len(entries))
		for _, e := range entries {
			if prev, ok := seen[e.id]; ok {
				if prev == e.title {
					continue
				}
				e.id += "#" + slug(e.title)
				if _, dup := seen[e.id]; dup {
					continue
				}
			}
			seen[e.id] = e.title
			scenes = append(scenes, s.toScene(studioURL, e, now))
		}
		_, more := nextArchive(archives, visited)
		return scraper.PageResult{Scenes: scenes, Done: !more, Continue: len(scenes) == 0}, nil
	})
}

func (s *Scraper) runModel(ctx context.Context, studioURL, path string, opts scraper.ListOpts, now time.Time, out chan<- scraper.SceneResult) {
	pageURL := s.base + path
	body, err := s.fetch(ctx, pageURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	entries := parseListing(body, pageURL)
	if len(entries) == 0 {
		select {
		case out <- scraper.Error(scraper.ParseError(pageURL, fmt.Errorf("no scenes found on model page"))):
		case <-ctx.Done():
		}
		return
	}
	name := modelName(body)
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.id] || opts.KnownIDs[e.id] {
			continue
		}
		seen[e.id] = true
		if name != "" {
			e.performers = []string{name}
		}
		select {
		case out <- scraper.Scene(s.toScene(studioURL, e, now)):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) toScene(studioURL string, e entry, now time.Time) models.Scene {
	return models.Scene{
		ID:          e.id,
		SiteID:      s.cfg.id,
		StudioURL:   studioURL,
		Title:       e.title,
		URL:         e.url,
		Date:        e.date,
		Description: e.description,
		Thumbnail:   e.thumbnail,
		Preview:     e.preview,
		Performers:  e.performers,
		Studio:      s.cfg.studio,
		ScrapedAt:   now,
	}
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return decode(raw), nil
}

// decode reads a page as UTF-8 when it is valid UTF-8 and as Windows-1252
// otherwise: the sites mix FrontPage-era pages in both.
func decode(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	b, err := charmap.Windows1252.NewDecoder().Bytes(raw)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

// nextArchive picks the highest-numbered archive page not yet visited. Higher
// numbers are newer on all four sites.
func nextArchive(archives map[int]string, visited map[int]bool) (int, bool) {
	keys := make([]int, 0, len(archives))
	for n := range archives {
		if !visited[n] {
			keys = append(keys, n)
		}
	}
	if len(keys) == 0 {
		return 0, false
	}
	sort.Ints(keys)
	return keys[len(keys)-1], true
}

func archiveLinks(body, prefix string) map[int]string {
	re := regexp.MustCompile(`(?i)href\s*=\s*"(` + regexp.QuoteMeta(prefix) + `_archive_?(\d+)\.htm)"`)
	links := map[int]string{}
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		links[n] = m[1]
	}
	return links
}

// ---- listing parser ----

type entry struct {
	id          string
	url         string
	title       string
	description string
	date        time.Time
	thumbnail   string
	preview     string
	performers  []string
}

type evKind int

const (
	evText evKind = iota
	evBreak
	evMember
	evPreview
	evImage
)

type event struct {
	kind evKind
	val  string
}

var (
	stripRe   = regexp.MustCompile(`(?is)<script.*?</script>|<style.*?</style>|<!--.*?-->`)
	tokenRe   = regexp.MustCompile(`(?s)<([^>]*)>|([^<]+)`)
	hrefRe    = regexp.MustCompile(`(?is)\bhref\s*=\s*"([^"]+)"`)
	srcRe     = regexp.MustCompile(`(?is)\bsrc\s*=\s*"([^"]+)"`)
	anchorRe  = regexp.MustCompile(`(?i)^a\s`)
	imgRe     = regexp.MustCompile(`(?i)^img\s`)
	blockRe   = regexp.MustCompile(`(?i)^/?(p|br|td|tr|div|table|li|h[1-6])\b`)
	memberRe  = regexp.MustCompile(`(?i)/members_[^/]+/.+\.html?$`)
	previewRe = regexp.MustCompile(`(?i)\.(mp4|m4v|mov|wmv)$`)
	spaceRe   = regexp.MustCompile(`\s+`)

	dateRe    = regexp.MustCompile(`(?i)^(?:update for\W*)?(january|february|march|april|may|june|july|august|september|october|november|december)\W*(\d{1,2})\W*(\d{4})(?:\s*-\s*[a-z][a-z ]{0,20})?\W*$`)
	numDateRe = regexp.MustCompile(`(?i)^update for (\d{1,2})-(\d{1,2})-(\d{4})$`)
	stopRe    = regexp.MustCompile(`(?i)^(archives page|the higher numbered|all images|past updates|members - double|after appearing)`)
	statsRe   = regexp.MustCompile(`(?i)^(.+?)'s stats:?$`)
	statRe    = regexp.MustCompile(`(?i)^(age|height|weight|hair|eyes|waist|inseam|shoe size|neck|chest|build)\s*:`)
	junkRe    = regexp.MustCompile(`(?i)^(video in standard|& ipod version|click on picture)|^[\W_]*$`)
)

func tokenize(body, pageURL string) []event {
	base, _ := url.Parse(pageURL)
	body = stripRe.ReplaceAllString(body, " ")
	var evs []event
	for _, m := range tokenRe.FindAllStringSubmatchIndex(body, -1) {
		if m[2] >= 0 {
			tag := strings.TrimSpace(body[m[2]:m[3]])
			switch {
			case anchorRe.MatchString(tag):
				h := hrefRe.FindStringSubmatch(tag)
				if h == nil {
					continue
				}
				abs := resolve(base, h[1])
				if abs == nil {
					continue
				}
				switch {
				case memberRe.MatchString(abs.Path):
					evs = append(evs, event{evMember, abs.String()})
				case previewRe.MatchString(abs.Path):
					evs = append(evs, event{evPreview, abs.String()})
				}
			case imgRe.MatchString(tag):
				if h := srcRe.FindStringSubmatch(tag); h != nil {
					if abs := resolve(base, h[1]); abs != nil {
						evs = append(evs, event{evImage, abs.String()})
					}
				}
			case blockRe.MatchString(tag):
				evs = append(evs, event{evBreak, ""})
			}
			continue
		}
		t := strings.ReplaceAll(html.UnescapeString(body[m[4]:m[5]]), " ", " ")
		if strings.TrimSpace(t) != "" {
			evs = append(evs, event{evText, t})
		}
	}
	return evs
}

func resolve(base *url.URL, href string) *url.URL {
	href = strings.ReplaceAll(strings.TrimSpace(html.UnescapeString(href)), " ", "%20")
	ref, err := url.Parse(href)
	if err != nil || base == nil {
		return nil
	}
	return base.ResolveReference(ref)
}

// parseListing walks a listing page as a flat stream of text blocks and
// links. Each members-area link opens a scene; the first text block after it
// is the title and the rest, up to the "Preview" link or the next date
// heading, the description. A date heading precedes its scene and applies to
// that scene only — older archive pages stop dating scenes partway down.
func parseListing(body, pageURL string) []entry {
	var (
		out       []entry
		cur       *entry
		closed    bool
		blocks    []string
		buf       strings.Builder
		date      time.Time
		performer string
	)
	finish := func() {
		if cur != nil && len(blocks) > 0 {
			cur.title = blocks[0]
			cur.description = strings.Join(blocks[1:], " ")
			if performer != "" {
				cur.performers = []string{performer}
			}
			out = append(out, *cur)
		}
		cur, blocks, performer, closed = nil, nil, "", false
	}
	flush := func() {
		t := strings.TrimSpace(spaceRe.ReplaceAllString(buf.String(), " "))
		buf.Reset()
		if t == "" || cur == nil || closed {
			return
		}
		switch {
		case junkRe.MatchString(t), statRe.MatchString(t):
		case statsRe.MatchString(t):
			performer = strings.TrimSpace(statsRe.FindStringSubmatch(t)[1])
		default:
			blocks = append(blocks, t)
		}
	}

	for _, ev := range tokenize(body, pageURL) {
		switch ev.kind {
		case evMember:
			flush()
			if cur != nil && cur.url == ev.val && len(blocks) == 0 && !closed {
				continue
			}
			finish()
			cur = &entry{url: ev.val, id: sceneID(ev.val), date: date}
			date = time.Time{}
		case evImage:
			if cur != nil && !closed && cur.thumbnail == "" && len(blocks) == 0 && buf.Len() == 0 {
				cur.thumbnail = ev.val
			}
		case evPreview:
			flush()
			if cur != nil && !closed {
				cur.preview = ev.val
			}
		case evBreak:
			flush()
		case evText:
			t := strings.TrimSpace(spaceRe.ReplaceAllString(ev.val, " "))
			if d, ok := parseDate(t); ok {
				flush()
				closed = true
				date = d
				continue
			}
			if cur == nil || closed {
				continue
			}
			if stopRe.MatchString(t) || strings.EqualFold(t, "preview") {
				flush()
				closed = true
				continue
			}
			buf.WriteString(ev.val)
		}
	}
	flush()
	finish()

	// Multi-part sets often link every part to one members page; the title
	// is what tells the parts apart.
	titles := map[string]map[string]bool{}
	for _, e := range out {
		if titles[e.id] == nil {
			titles[e.id] = map[string]bool{}
		}
		titles[e.id][e.title] = true
	}
	for i := range out {
		if len(titles[out[i].id]) > 1 {
			out[i].id += "#" + slug(out[i].title)
		}
	}
	return out
}

func parseDate(t string) (time.Time, bool) {
	if m := dateRe.FindStringSubmatch(t); m != nil {
		d, err := time.Parse("January 2 2006", m[1]+" "+m[2]+" "+m[3])
		if err == nil {
			return d.UTC(), true
		}
	}
	if m := numDateRe.FindStringSubmatch(t); m != nil {
		d, err := time.Parse("1-2-2006", m[1]+"-"+m[2]+"-"+m[3])
		if err == nil {
			return d.UTC(), true
		}
	}
	return time.Time{}, false
}

// sceneID is the members-area path below the members_<site>/ directory,
// lowercased and without extension — the one stable per-scene key the sites
// publish.
func sceneID(memberURL string) string {
	u, err := url.Parse(memberURL)
	if err != nil {
		return strings.ToLower(memberURL)
	}
	p := u.Path
	if i := strings.Index(strings.ToLower(p), "/members_"); i >= 0 {
		p = p[i+1:]
		if j := strings.Index(p, "/"); j >= 0 {
			p = p[j+1:]
		}
	}
	p = strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(p), ".htm"), ".html")
	return p
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// modelName is the heading text immediately before a model page's first scene.
func modelName(body string) string {
	evs := tokenize(body, "https://example.invalid/")
	name := ""
	for _, ev := range evs {
		switch ev.kind {
		case evMember:
			return name
		case evText:
			if t := strings.TrimSpace(spaceRe.ReplaceAllString(ev.val, " ")); t != "" {
				name = t
			}
		}
	}
	return ""
}
