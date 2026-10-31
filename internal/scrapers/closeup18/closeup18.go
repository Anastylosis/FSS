// Package closeup18 scrapes the 18closeup.com update listing.
package closeup18

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "closeup18"
	studioName = "18 Close Up"
	pageSize   = 100
)

var siteBase = "https://18closeup.com"

type Scraper struct {
	client *http.Client
}

func New() *Scraper { return &Scraper{client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"18closeup.com",
		"18closeup.com/updates.php",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?18closeup\.com(?:/(?:updates\.php)?)?(?:[?#].*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.Debugf(1, "closeup18: walking the update listing, %d a page", pageSize)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/updates.php?sort1=date&ascending=0&solo=0&les=0&show=%d&search=&page=%d", siteBase, pageSize, page)
		resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
		if err != nil {
			return scraper.PageResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := httpx.ReadBody(resp.Body)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parsePage(body, studioURL, now)
		return scraper.PageResult{Scenes: scenes, Done: len(scenes) < pageSize}, nil
	})
}

var (
	itemRe    = regexp.MustCompile(`(?s)<li>\s*<a href="[^"]*" class="highslide".*?</li>`)
	thumbRe   = regexp.MustCompile(`<img id="img\d+" src="([^"]+)"`)
	modelRe   = regexp.MustCompile(`<name>([^<]*)<br><span>([\d:]+)</span>`)
	updateRe  = regexp.MustCompile(`<time>Update (\d+)<br>([^<:]*):`)
	descRe    = regexp.MustCompile(`(?s)</div>\s*<p>(.*?)</p>`)
	absolutes = regexp.MustCompile(`^(?:\.\./|\./|/)+`)
)

func parsePage(body []byte, studioURL string, now time.Time) []models.Scene {
	var out []models.Scene
	for _, blk := range itemRe.FindAll(body, -1) {
		u := updateRe.FindSubmatch(blk)
		if u == nil {
			continue
		}
		sc := models.Scene{
			ID:         string(u[1]),
			SiteID:     siteID,
			StudioURL:  studioURL,
			URL:        siteBase + "/updates.php",
			Studio:     studioName,
			Resolution: strings.TrimSpace(string(u[2])),
			ScrapedAt:  now,
		}
		if m := modelRe.FindSubmatch(blk); m != nil {
			name := strings.TrimSpace(html.UnescapeString(string(m[1])))
			sc.Title = name + " - Update " + sc.ID
			sc.Performers = []string{name}
			sc.Duration = parseutil.ParseDurationColon(string(m[2]))
		}
		if m := thumbRe.FindSubmatch(blk); m != nil {
			sc.Thumbnail = siteBase + "/" + absolutes.ReplaceAllString(string(m[1]), "")
		}
		if m := descRe.FindSubmatch(blk); m != nil {
			sc.Description = strings.Join(strings.Fields(html.UnescapeString(string(m[1]))), " ")
		}
		if sc.Title == "" {
			sc.Title = "Update " + sc.ID
		}
		out = append(out, sc)
	}
	return out
}
