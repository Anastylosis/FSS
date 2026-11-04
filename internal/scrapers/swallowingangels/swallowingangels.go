// Package swallowingangels scrapes Swallowing Angels (swallowingangels.com), a
// small classic-ASP site. /all_titles.asp lists every title and each
// /title.asp?id=saNNNN page carries the runtime, release date, performers and
// synopsis. See docs/scrapers.md.
package swallowingangels

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "swallowingangels"
	studioName = "Swallowing Angels"
)

var siteBase = "https://www.swallowingangels.com"

var thumbBase = "https://sapublic.b-cdn.net"

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?swallowingangels\.com(?:/|$)`)

	titleRowRe = regexp.MustCompile(`href="title\.asp\?id=(sa\d+)"[^>]*>([^<]*)</a>`)
	h1Re       = regexp.MustCompile(`<h1>(.*?)</h1>`)
	yearRe     = regexp.MustCompile(`\s*\(\d{4}\)\s*$`)
	fieldRe    = regexp.MustCompile(`(?s)class="bioheading">([^<]+)</td></tr>\s*<tr[^>]*><td[^>]*class="biodata">([^<]*)`)
	performRe  = regexp.MustCompile(`href="performer\.asp\?id=\d+">([^<]+)</a>`)
	synopsisRe = regexp.MustCompile(`(?s)>Synopsis</td></tr>\s*<tr[^>]*><td[^>]*class="biodata">(.*?)</td></tr>`)
	tagRe      = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string { return []string{"swallowingangels.com"} }

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type entry struct{ id, title string }

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	body, err := s.fetch(ctx, siteBase+"/all_titles.asp")
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	entries := parseTitles(body)
	if len(entries) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(siteBase+"/all_titles.asp", fmt.Errorf("no titles found"))))
		return
	}
	scraper.Debugf(1, "%s: %d titles listed", siteID, len(entries))
	if !send(ctx, out, scraper.Progress(len(entries))) {
		return
	}

	now := time.Now().UTC()
	for _, e := range entries {
		if opts.KnownIDs[e.id] {
			continue
		}
		if !scraper.Pace(ctx, opts.Delay) {
			return
		}
		u := siteBase + "/title.asp?id=" + e.id
		page, err := s.fetch(ctx, u)
		if err != nil {
			if !send(ctx, out, scraper.Error(fmt.Errorf("detail %s: %w", u, err))) {
				return
			}
			continue
		}
		if !send(ctx, out, scraper.Scene(toScene(e, page, studioURL, u, now))) {
			return
		}
	}
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func parseTitles(body []byte) []entry {
	var out []entry
	seen := map[string]bool{}
	for _, m := range titleRowRe.FindAllSubmatch(body, -1) {
		id := string(m[1])
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, entry{id: id, title: clean(string(m[2]))})
	}
	return out
}

func toScene(e entry, body []byte, studioURL, u string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:        e.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     e.title,
		URL:       u,
		Thumbnail: fmt.Sprintf("%s/%s/%s.1.jpg", thumbBase, e.id, e.id),
		Studio:    studioName,
		ScrapedAt: now,
	}
	if m := h1Re.FindSubmatch(body); m != nil {
		if t := clean(yearRe.ReplaceAllString(clean(string(m[1])), "")); t != "" {
			sc.Title = t
		}
	}
	for _, m := range fieldRe.FindAllSubmatch(body, -1) {
		v := clean(string(m[2]))
		switch strings.TrimSpace(string(m[1])) {
		case "MINUTES":
			if n, err := strconv.Atoi(v); err == nil {
				sc.Duration = n * 60
			}
		case "RELEASE DATE":
			if d, err := parseutil.TryParseDate(v, "Jan 2, 2006"); err == nil {
				sc.Date = d.UTC()
			}
		}
	}
	for _, m := range performRe.FindAllSubmatch(body, -1) {
		sc.Performers = append(sc.Performers, clean(string(m[1])))
	}
	if m := synopsisRe.FindSubmatch(body); m != nil {
		sc.Description = clean(string(m[1]))
	}
	return sc
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(s, " "))), " ")
}
