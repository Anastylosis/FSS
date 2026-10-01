package glamose

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

var portalBase = "https://www.glamose.com"

type portalScraper struct {
	client *http.Client
}

var _ scraper.StudioScraper = (*portalScraper)(nil)

var portalMatchRe = regexp.MustCompile(`^https?://(?:www\.)?glamose\.com(?:/|$)`)

func (s *portalScraper) ID() string               { return "glamose" }
func (s *portalScraper) Patterns() []string       { return []string{"glamose.com/"} }
func (s *portalScraper) MatchesURL(u string) bool { return portalMatchRe.MatchString(u) }

func (s *portalScraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.runPortal(ctx, studioURL, opts, out)
	return out, nil
}

var (
	boxSplitRe    = regexp.MustCompile(`<div class="box"[\s>]`)
	updateIDRe    = regexp.MustCompile(`update_id=(\d+)`)
	boxModelRe    = regexp.MustCompile(`/model/[^"]*"[^>]*>([^<]+)</a>`)
	boxSiteRe     = regexp.MustCompile(`<span class="site">([^<]+)</span>`)
	boxDateRe     = regexp.MustCompile(`<span class="date">([^<]+)</span>`)
	boxImgRe      = regexp.MustCompile(`(?:data-src|src)="((?:https?:)?//cdn\.glamose\.com/[^"]+)"`)
	boxVideoRe    = regexp.MustCompile(`play-icon|icons/play\.png`)
	portalTotalRe = regexp.MustCompile(`<title>[^<]*\(([\d,]+)\)`)
)

// runPortal walks the portal's `?start=N` offset pager. The offset advances by
// the number of boxes actually served rather than a fixed page size: the page
// size has changed before (50 to 30), and a stale constant skips updates.
func (s *portalScraper) runPortal(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	offset := 0
	scraper.Paginate(ctx, opts, "glamose", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/?start=%d", portalBase, offset)

		body, err := s.fetchPortalHTML(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}

		boxes := portalBoxes(body)
		if len(boxes) == 0 {
			return scraper.PageResult{}, nil
		}
		offset += len(boxes)

		total := 0
		if page == 1 {
			total = parsePortalTotal(body)
		}
		return scraper.PageResult{
			Scenes:   parseBoxes(boxes, studioURL),
			Total:    total,
			Continue: true,
		}, nil
	})
}

func portalBoxes(body []byte) [][]byte {
	idx := boxSplitRe.FindAllIndex(body, -1)
	boxes := make([][]byte, len(idx))
	for i, loc := range idx {
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		boxes[i] = body[loc[0]:end]
	}
	return boxes
}

func parsePortalTotal(body []byte) int {
	m := portalTotalRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.ReplaceAll(string(m[1]), ",", ""))
	return n
}

func parsePortalPage(body []byte, studioURL string) []models.Scene {
	return parseBoxes(portalBoxes(body), studioURL)
}

func parseBoxes(boxes [][]byte, studioURL string) []models.Scene {
	now := time.Now().UTC()
	var scenes []models.Scene

	for _, box := range boxes {
		content := box

		m := updateIDRe.FindSubmatch(content)
		if m == nil {
			continue
		}
		uid := string(m[1])

		scene := models.Scene{
			ID:        uid,
			SiteID:    "glamose",
			StudioURL: studioURL,
			URL:       portalBase + "/?update_id=" + uid,
			Studio:    "Glamose",
			ScrapedAt: now,
		}

		if m := boxModelRe.FindSubmatch(content); m != nil {
			name := strings.TrimSpace(html.UnescapeString(string(m[1])))
			scene.Title = name
			scene.Performers = []string{name}
		}

		if m := boxSiteRe.FindSubmatch(content); m != nil {
			scene.Series = strings.TrimSpace(string(m[1]))
		}

		if m := boxDateRe.FindSubmatch(content); m != nil {
			dateStr := strings.TrimSpace(string(m[1]))
			cleaned := parseutil.StripOrdinalSuffix(dateStr)
			for _, layout := range []string{"2 Jan 2006", "2 January 2006"} {
				if t, err := time.Parse(layout, cleaned); err == nil {
					scene.Date = t.UTC()
					break
				}
			}
		}

		if m := boxImgRe.FindSubmatch(content); m != nil {
			thumb := string(m[1])
			if strings.HasPrefix(thumb, "//") {
				thumb = "https:" + thumb
			}
			scene.Thumbnail = thumb
		}

		if boxVideoRe.Match(content) {
			scene.Tags = []string{"Video"}
		}

		scenes = append(scenes, scene)
	}
	return scenes
}

func (s *portalScraper) fetchPortalHTML(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: rawURL,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
