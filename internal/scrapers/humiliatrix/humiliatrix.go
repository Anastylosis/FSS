// Package humiliatrix scrapes humiliatrix.com, whose whole catalogue is one
// hand-built table page with no per-scene URL. See docs/scrapers.md.
package humiliatrix

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
	siteID     = "humiliatrix"
	studioName = "Humiliatrix"
	// The site answers on http; https is not served.
	defaultBase = "http://www.humiliatrix.com"
	listingPath = "/recentupdates.html"
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?humiliatrix\.com(?:/|$)`)

	// Each update opens with its own 384x216 thumbnail, whose filename stem is
	// the site's per-update code and the only stable id on the page.
	blockRe   = regexp.MustCompile(`<img src="(images/([A-Za-z0-9_]+)\.gif)"[^>]*width="384" height="216"`)
	titleRe   = regexp.MustCompile(`(?s)<span class="header">(.*?)</span>`)
	blurbRe   = regexp.MustCompile(`(?s)class="style22">(.*?)</p>`)
	creditsRe = regexp.MustCompile(`(?s)class="style23">(.*?)</div>`)
	dateRe    = regexp.MustCompile(`(?s)<span class="date11">(.*?)</span>`)
	previewRe = regexp.MustCompile(`href="(videopreview/[^"]+)"`)
	minutesRe = regexp.MustCompile(`(\d+)\s*minutes?`)

	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

// dateLayouts cover the page's "June 1 2019", written with and without a comma
// and with either a padded or an unpadded day.
var dateLayouts = []string{"January 2 2006", "January 2, 2006", "Jan 2 2006", "Jan 2, 2006"}

// Scraper implements scraper.StudioScraper for humiliatrix.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Humiliatrix scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: defaultBase}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"humiliatrix.com", "humiliatrix.com/recentupdates.html"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	pageURL := s.base + listingPath
	body, err := s.fetch(ctx, pageURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}

	scenes := parseListing(body, s.base, pageURL, studioURL, time.Now().UTC())
	if len(scenes) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(pageURL,
			fmt.Errorf("no update blocks on the listing page"))))
		return
	}

	scraper.Debugf(1, "%s: %d updates on the listing page", siteID, len(scenes))
	if !send(ctx, out, scraper.Progress(len(scenes))) {
		return
	}
	// The page runs newest-first, so the first known id ends the run.
	for _, sc := range scenes {
		if opts.KnownIDs[sc.ID] {
			scraper.Debugf(1, "%s: hit known ID, stopping early", siteID)
			send(ctx, out, scraper.StoppedEarly())
			return
		}
		if !send(ctx, out, scraper.Scene(sc)) {
			return
		}
	}
}

// parseListing reads the update blocks. They are sliced between the thumbnail
// markers that open them — the markup is one deeply nested table with no
// per-update container to close on.
func parseListing(body, base, pageURL, studioURL string, now time.Time) []models.Scene {
	marks := blockRe.FindAllStringSubmatchIndex(body, -1)
	scenes := make([]models.Scene, 0, len(marks))
	for i, m := range marks {
		end := len(body)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		block := body[m[1]:end]

		t := titleRe.FindStringSubmatch(block)
		if t == nil {
			continue
		}
		title := cleanText(t[1])
		if title == "" {
			continue
		}

		sc := models.Scene{
			ID:        body[m[4]:m[5]],
			SiteID:    siteID,
			StudioURL: studioURL,
			Studio:    studioName,
			Title:     title,
			// There is no per-scene page: every link on the block goes to the
			// join form, so the listing is where the scene lives.
			URL:       pageURL + "#" + body[m[4]:m[5]],
			Thumbnail: base + "/" + body[m[2]:m[3]],
			ScrapedAt: now,
		}
		if b := blurbRe.FindStringSubmatch(block); b != nil {
			blurb := cleanText(b[1])
			sc.Duration = parseMinutes(blurb)
			sc.Description = strings.TrimSpace(minutesRe.ReplaceAllString(blurb, ""))
			sc.Description = strings.TrimRight(sc.Description, " .")
		}
		if c := creditsRe.FindStringSubmatch(block); c != nil {
			sc.Performers, sc.Tags = parseCredits(cleanText(c[1]))
		}
		if d := dateRe.FindStringSubmatch(block); d != nil {
			if parsed, err := parseutil.TryParseDate(cleanText(d[1]), dateLayouts...); err == nil {
				sc.Date = parsed
			}
		}
		if p := previewRe.FindStringSubmatch(block); p != nil {
			sc.Preview = base + "/" + p[1]
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

// parseCredits splits the site's one credit line: the first comma-separated
// item is who is in the scene, the rest are its keywords. The credit is stored
// as published, so a performer billed under several epithets ("Princess
// Ashleigh", "Sadistic Princess Ashleigh") is stored under each.
func parseCredits(line string) (performers, tags []string) {
	parts := strings.Split(strings.TrimRight(line, ". "), ",")
	for i, p := range parts {
		p = strings.TrimSpace(strings.TrimRight(p, "."))
		if p == "" {
			continue
		}
		if i == 0 {
			performers = append(performers, p)
			continue
		}
		tags = append(tags, p)
	}
	return performers, tags
}

// parseMinutes reads the runtime the blurb ends with ("… 13 minutes").
func parseMinutes(blurb string) int {
	m := minutesRe.FindStringSubmatch(blurb)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n * 60
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

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}
