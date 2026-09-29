package queensnake

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

const defaultSiteBase = "https://queensnake.com"

type Scraper struct {
	client   *http.Client
	siteBase string
}

func New() *Scraper {
	return &Scraper{
		client:   httpx.NewClient(30 * time.Second),
		siteBase: defaultSiteBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "queensnake" }

func (s *Scraper) Patterns() []string {
	return []string{"queensnake.com"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?queensnake\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()

	for page := 0; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 0 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "queensnake: fetching page %d", page)

		pageURL := fmt.Sprintf("%s/previewmovies/%d", s.siteBase, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		scenes := parseSceneBlocks(body)
		if len(scenes) == 0 {
			return
		}

		if page == 0 {
			total := estimateTotal(body, len(scenes))
			if total > 0 {
				scraper.Debugf(1, "queensnake: %d total scenes", total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
		}

		for _, ps := range scenes {
			if opts.KnownIDs[ps.filmID] {
				scraper.Debugf(1, "queensnake: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}

			scene := toScene(ps, s.siteBase, studioURL, now)
			select {
			case out <- scraper.Scene(scene):
			case <-ctx.Done():
				return
			}
		}

		if !hasNextPage(body) {
			return
		}
	}
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: url,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Cookie"] = "cLegalAge=true"
			return h
		}(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

type parsedScene struct {
	filmID      string
	title       string
	date        string
	duration    string
	description string
	tags        []string
	thumbnail   string
}

var (
	blockStartRe = regexp.MustCompile(`<div\s+class="contentFilmNameSeal"\s+data-filmid="([^"]+)"\s+data-isonline="[^"]*"\s*"?\s*data-onlinedate="([^"]*)"`)
	titleRe      = regexp.MustCompile(`class="contentFilmName">\s*([^<]+)`)
	dateDurRe    = regexp.MustCompile(`class="contentFileDate">\s*([^<]+)`)
	descRe       = regexp.MustCompile(`(?s)class="contentPreviewDescription">\s*(.*?)\s*</div>`)
	tagRe        = regexp.MustCompile(`class="contentPreviewTags">([^§]*?)</div>`)
	tagLinkRe    = regexp.MustCompile(`>([^<]+)</a>`)
	thumbRe      = regexp.MustCompile(`preview/([^/]+)/[^"]*-prev0\.jpg\?v=([^"&\s]+)`)
	pagerMaxRe   = regexp.MustCompile(`previewmovies/(\d+)`)
	nextPageRe   = regexp.MustCompile(`class="pagerarrowRight"`)
)

func parseSceneBlocks(body []byte) []parsedScene {
	locs := blockStartRe.FindAllSubmatchIndex(body, -1)
	scenes := make([]parsedScene, 0, len(locs))

	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		filmID := string(body[loc[2]:loc[3]])

		ps := parsedScene{
			filmID: filmID,
		}

		if m := titleRe.FindSubmatch(block); m != nil {
			ps.title = html.UnescapeString(strings.TrimSpace(string(m[1])))
		}

		if m := dateDurRe.FindSubmatch(block); m != nil {
			parts := strings.SplitN(strings.TrimSpace(string(m[1])), "•", 2)
			ps.date = strings.TrimSpace(parts[0])
			if len(parts) > 1 {
				ps.duration = strings.TrimSpace(parts[1])
			}
		}

		if m := descRe.FindSubmatch(block); m != nil {
			ps.description = cleanDescription(string(m[1]))
		}

		if m := tagRe.FindSubmatch(block); m != nil {
			for _, tm := range tagLinkRe.FindAllSubmatch(m[1], -1) {
				tag := strings.TrimSpace(string(tm[1]))
				if tag != "" {
					ps.tags = append(ps.tags, tag)
				}
			}
		}

		if m := thumbRe.FindSubmatch(block); m != nil {
			slug := string(m[1])
			ver := string(m[2])
			ps.thumbnail = fmt.Sprintf("https://cdn.queensnake.com/preview/%s/%s-prev0-2560.jpg?v=%s", slug, slug, ver)
		}

		scenes = append(scenes, ps)
	}

	return scenes
}

func estimateTotal(body []byte, perPage int) int {
	maxPage := 0
	for _, m := range pagerMaxRe.FindAllSubmatch(body, -1) {
		if n, _ := strconv.Atoi(string(m[1])); n > maxPage {
			maxPage = n
		}
	}
	return (maxPage + 1) * perPage
}

func hasNextPage(body []byte) bool {
	return nextPageRe.Match(body)
}

func toScene(ps parsedScene, siteBase, studioURL string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:          ps.filmID,
		SiteID:      "queensnake",
		StudioURL:   studioURL,
		Title:       ps.title,
		URL:         siteBase + "/previewmovie/" + ps.filmID,
		Thumbnail:   ps.thumbnail,
		Description: ps.description,
		Performers:  extractPerformers(ps.title, ps.tags),
		Tags:        ps.tags,
		Date:        parseDate(ps.date),
		Duration:    parseDuration(ps.duration),
		Studio:      "Queensnake",
		ScrapedAt:   now,
	}
	// No price snapshot: the site exposes no pricing on these pages, and a
	// zero-value "not free, amount unknown" entry records nothing.
	return scene
}

func extractPerformers(title string, tags []string) []string {
	titleUpper := strings.ToUpper(strings.TrimSpace(title))

	var performers []string
	for _, tag := range tags {
		upper := strings.ToUpper(tag)
		if strings.Contains(titleUpper, upper) && upper != titleUpper {
			performers = append(performers, tag)
		}
	}
	return performers
}

var brRe = regexp.MustCompile(`(?i)<br\s*/?>`)
var multiNlRe = regexp.MustCompile(`\n{3,}`)

func cleanDescription(s string) string {
	s = brRe.ReplaceAllString(s, "\n")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	s = strings.Join(lines, "\n")
	s = multiNlRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	t, err := time.Parse("2006 January 2", s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func parseDuration(s string) int {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, " min")
	n, _ := strconv.Atoi(s)
	return n * 60
}
