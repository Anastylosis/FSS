// Package restrainedelegance scrapes restrainedelegance.com's public updates
// archive. See docs/scrapers.md.
package restrainedelegance

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
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
	siteID     = "restrainedelegance"
	studioName = "Restrained Elegance"
	siteBase   = "https://www.restrainedelegance.com"

	// fetchAttempts bounds the age-gate and session bootstrap round trips for
	// one page.
	fetchAttempts = 4
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?restrainedelegance\.com(?:/|$)`)

	entrySplit = "<td width='450' valign='top'>"

	titleRe     = regexp.MustCompile(`(?s)^<h1[^>]*>(.*?)</h1>`)
	kindRe      = regexp.MustCompile(`(?s)</h1>\s*(?:<h2>[^<]*</h2>\s*)?(\w+) \((\d+:\d+) minute video\)(.*?)<h3`)
	castRe      = regexp.MustCompile(`<a href='/modelbio\.php\?id=[^']*'>([^<]+)</a>`)
	addedRe     = regexp.MustCompile(`<h3>Added (\d{2}-\d{2}-\d{4})</h3>`)
	tagsRe      = regexp.MustCompile(`Tags: ([^<]*)</p>`)
	thumbRe     = regexp.MustCompile(`</td><td width='300'[^>]*>\s*<a href='[^']*'><img src='(/images/([^']+?)i?\.jpg)'`)
	pageLinkRe  = regexp.MustCompile(`whatsnew\.php\?p=(\d+)`)
	paraRe      = regexp.MustCompile(`(?s)<p>(.*?)</p>`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
	modelPathRe = regexp.MustCompile(`^/modelbio\.php$`)
)

// Scraper implements scraper.StudioScraper for restrainedelegance.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Restrained Elegance scraper.
func New() *Scraper {
	jar, _ := cookiejar.New(nil)
	c := httpx.NewClient(45 * time.Second)
	c.Jar = jar
	return &Scraper{client: c, base: siteBase}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"restrainedelegance.com",
		"restrainedelegance.com/whatsnew.php",
		"restrainedelegance.com/modelbio.php?id={name}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// resolveModel returns the performer an operator URL selects, or "" for the
// whole catalogue.
func resolveModel(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil || !modelPathRe.MatchString(u.Path) {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("id"))
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	model := resolveModel(studioURL)
	if model != "" {
		scraper.Debugf(1, "%s: filtering the updates archive to %q", siteID, model)
	}
	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/whatsnew.php?p=%d", s.base, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		entries := parsePage(body, s.base)
		if page == 1 && len(entries) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no update entries on the first page"))
		}

		var scenes []models.Scene
		for _, sc := range entries {
			if seen[sc.ID] || (model != "" && !hasPerformer(sc.Performers, model)) {
				continue
			}
			seen[sc.ID] = true
			sc.SiteID = siteID
			sc.StudioURL = studioURL
			sc.Studio = studioName
			sc.ScrapedAt = now
			scenes = append(scenes, sc)
		}
		// A page of photosets alone is not the end of the archive; the
		// pager's last page number is.
		return scraper.PageResult{Scenes: scenes, Continue: true, Done: page >= lastPage(body)}, nil
	})
}

// lastPage reads the pager's highest page number. The site serves its last
// page again for any larger number, so the pager is the only end marker.
func lastPage(body string) int {
	last := 1
	for _, m := range pageLinkRe.FindAllStringSubmatch(body, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > last {
			last = n
		}
	}
	return last
}

func hasPerformer(performers []string, name string) bool {
	for _, p := range performers {
		if strings.EqualFold(p, name) {
			return true
		}
	}
	return false
}

// parsePage returns the page's video entries; photosets, artwork and
// slideshows share the markup and are dropped.
func parsePage(body, base string) []models.Scene {
	parts := strings.Split(body, entrySplit)
	scenes := make([]models.Scene, 0, len(parts))
	for _, blk := range parts[1:] {
		sc, ok := parseEntry(blk, base)
		if ok {
			scenes = append(scenes, sc)
		}
	}
	return scenes
}

func parseEntry(blk, base string) (models.Scene, bool) {
	k := kindRe.FindStringSubmatch(blk)
	t := titleRe.FindStringSubmatch(blk)
	th := thumbRe.FindStringSubmatch(blk)
	if k == nil || t == nil || th == nil || k[1] != "Video" {
		return models.Scene{}, false
	}
	sc := models.Scene{
		ID:        th[2],
		Title:     cleanTitle(t[1]),
		URL:       base + "/whatsnew.php#" + th[2],
		Thumbnail: base + th[1],
		Duration:  parseutil.ParseDurationColon(k[2]),
	}
	for _, m := range castRe.FindAllStringSubmatch(k[3], -1) {
		sc.Performers = append(sc.Performers, cleanText(m[1]))
	}
	if d := addedRe.FindStringSubmatch(blk); d != nil {
		if parsed, err := time.Parse("02-01-2006", d[1]); err == nil {
			sc.Date = parsed.UTC()
		}
	}
	if tg := tagsRe.FindStringSubmatch(blk); tg != nil {
		for _, tag := range strings.Split(html.UnescapeString(tg[1]), ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				sc.Tags = append(sc.Tags, tag)
			}
		}
	}
	sc.Description = description(blk)
	return sc, true
}

// description joins the story paragraphs that follow the tag line. The
// italic "read the full story inside" teaser used on archive entries is not
// one.
func description(blk string) string {
	i := strings.Index(blk, "Tags: ")
	if i < 0 {
		return ""
	}
	rest := blk[i:]
	if j := strings.Index(rest, "</p>"); j >= 0 {
		rest = rest[j+len("</p>"):]
	}
	if j := strings.Index(rest, "<div"); j >= 0 {
		rest = rest[:j]
	}
	if j := strings.Index(rest, "</td>"); j >= 0 {
		rest = rest[:j]
	}
	var paras []string
	for _, m := range paraRe.FindAllStringSubmatch(rest, -1) {
		if p := cleanText(m[1]); p != "" {
			paras = append(paras, p)
		}
	}
	return strings.Join(paras, "\n\n")
}

func cleanTitle(s string) string {
	s = cleanText(s)
	s = strings.TrimSpace(strings.TrimPrefix(s, "Archive:"))
	return s
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

// fetch loads a page through the age gate. The site answers a cookieless
// client with the disclaimer form, or with an empty page while it opens a
// session, and the form's POST sets the cookie the jar then carries.
func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	for attempt := 0; attempt < fetchAttempts; attempt++ {
		body, err := s.get(ctx, pageURL, nil)
		if err != nil {
			return "", err
		}
		switch {
		case strings.Contains(body, "age-disclaimer-form"):
			scraper.Debugf(1, "%s: accepting the age disclaimer", siteID)
			form := url.Values{
				"confirm_age":           {"1"},
				"confirm_terms":         {"1"},
				"age_disclaimer_submit": {"1"},
			}
			if body, err = s.get(ctx, pageURL, []byte(form.Encode())); err != nil {
				return "", err
			}
			if !strings.Contains(body, "age-disclaimer-form") && strings.TrimSpace(body) != "" {
				return body, nil
			}
		case strings.TrimSpace(body) != "":
			return body, nil
		}
	}
	return "", scraper.ParseError(pageURL, fmt.Errorf("no page content after the age gate"))
}

func (s *Scraper) get(ctx context.Context, pageURL string, form []byte) (string, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentChrome)
	req := httpx.Request{URL: pageURL, Headers: headers}
	if form != nil {
		req.Method = http.MethodPost
		req.Body = form
		headers["Content-Type"] = "application/x-www-form-urlencoded"
	}
	resp, err := httpx.Do(ctx, s.client, req)
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
