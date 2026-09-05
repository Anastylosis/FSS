package scoregrouputil

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	SiteID     string
	SiteBase   string
	StudioName string
	VideosPath string // e.g. "/xxx-milf-videos/"
	ModelsPath string // e.g. "/xxx-milf-models/"
}

type Scraper struct {
	Client *http.Client
	cfg    SiteConfig
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second), cfg: cfg}
}
func (s *Scraper) Run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	type detailWork struct {
		listing listingScene
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan detailWork)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for dw := range work {
				scene, err := s.fetchDetail(ctx, dw.listing, opts.Delay)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	singlePage := s.isModelURL(studioURL)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)
		for page := 1; ; page++ {
			var url string
			if singlePage {
				scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)
				url = stripNATS(studioURL)
			} else {
				url = fmt.Sprintf("%s%s?page=%d", s.cfg.SiteBase, s.cfg.VideosPath, page)
			}

			body, err := s.fetchPage(ctx, url)
			if err != nil {
				select {
				case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
				case <-ctx.Done():
				}
				return
			}

			scenes := parseListingPage(body, s.cfg.SiteBase)
			if page == 1 {
				if singlePage {
					select {
					case out <- scraper.Progress(len(scenes)):
					case <-ctx.Done():
						return
					}
				} else {
					totalPages := extractMaxPage(body)
					total := len(scenes) * totalPages
					scraper.Debugf(1, "%s: %d total scenes", s.cfg.SiteID, total)
					select {
					case out <- scraper.Progress(total):
					case <-ctx.Done():
						return
					}
				}
			}

			if len(scenes) == 0 {
				return
			}

			for _, sc := range scenes {
				if opts.KnownIDs[sc.id] {
					scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
					select {
					case out <- scraper.StoppedEarly():
					case <-ctx.Done():
					}
					return
				}
				select {
				case work <- detailWork{listing: sc}:
				case <-ctx.Done():
					return
				}
			}

			if singlePage || !hasNextPage(body, page) {
				return
			}

			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	wg.Wait()
}

func (s *Scraper) isModelURL(u string) bool {
	if s.cfg.ModelsPath != "" && strings.Contains(u, s.cfg.ModelsPath) {
		return true
	}
	return false
}

func stripNATS(u string) string {
	if idx := strings.Index(u, "?nats="); idx > 0 {
		return u[:idx]
	}
	if idx := strings.Index(u, "&nats="); idx > 0 {
		return u[:idx]
	}
	return u
}

type listingScene struct {
	id         string
	url        string
	title      string
	performers []string
	duration   int
	thumb      string
}

var (
	sceneStartRe = regexp.MustCompile(`class="li-item (?:compact video|video-wide[^"]*|movie)"`)
	sceneLinkRe  = regexp.MustCompile(`href="(https?://[^"]*?/(\d+)/?(?:\?[^"]*)?)"`)
	titleRe      = regexp.MustCompile(`class="i-title[^"]*">\s*([^<\s][^<]*)`)
	modelRe      = regexp.MustCompile(`class="i-model"[^>]*>([^<]+)`)
	durationRe   = regexp.MustCompile(`class="time-ol[^"]*"[^>]*>(\d+):(\d+)\s*mins?`)
	thumbRe      = regexp.MustCompile(`src="((?:https?:)?//[^"]*(?:posting_\d+|scoreuniverse\.com/)[^"]*\.(?:jpg|png|webp))"`)
)

func parseListingPage(body []byte, _ string) []listingScene {
	html := string(body)
	locs := sceneStartRe.FindAllStringIndex(html, -1)
	var scenes []listingScene

	for i, loc := range locs {
		start := loc[0]
		end := len(html)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := html[start:end]

		linkM := sceneLinkRe.FindStringSubmatch(block)
		if linkM == nil {
			continue
		}
		rawURL := linkM[1]
		id := linkM[2]

		// Strip NATS tracking from URL
		cleanURL := rawURL
		if idx := strings.Index(rawURL, "?nats="); idx > 0 {
			cleanURL = rawURL[:idx+1]
		}
		cleanURL = strings.TrimSuffix(cleanURL, "?")
		if !strings.HasSuffix(cleanURL, "/") {
			cleanURL += "/"
		}

		titleM := titleRe.FindStringSubmatch(block)
		title := ""
		if titleM != nil {
			title = strings.TrimSpace(titleM[1])
		}

		modelM := modelRe.FindStringSubmatch(block)
		var performers []string
		if modelM != nil {
			for _, p := range strings.Split(modelM[1], ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					performers = append(performers, p)
				}
			}
		}

		dur := 0
		durM := durationRe.FindStringSubmatch(block)
		if durM != nil {
			mins, _ := strconv.Atoi(durM[1])
			secs, _ := strconv.Atoi(durM[2])
			dur = mins*60 + secs
		}

		thumbM := thumbRe.FindStringSubmatch(block)
		thumb := ""
		if thumbM != nil {
			thumb = thumbM[1]
		}

		scenes = append(scenes, listingScene{
			id:         id,
			url:        cleanURL,
			title:      title,
			performers: performers,
			duration:   dur,
			thumb:      thumb,
		})
	}
	return scenes
}

// The pager renders its links as a bare query (`<a href="?page=3">`), while
// tag and model links carry a path in front of theirs. Reading `page=` off the
// whole page counted those too, which inflated the scene total reported to the
// progress line — `len(scenes) * totalPages` — by however many such links a
// listing happened to carry.
//
// pagerPageRe is preferred, but a page where it finds nothing falls back to the
// loose form rather than concluding there is one page: an absolute-href pager
// would otherwise end every walk after page one, and a silent single-page
// scrape is far worse than an over-counted total.
var (
	pagerPageRe = regexp.MustCompile(`href="\?page=(\d+)"`)
	pageRe      = regexp.MustCompile(`page=(\d+)`)
)

func pageNumbers(body []byte) [][][]byte {
	if m := pagerPageRe.FindAllSubmatch(body, -1); len(m) > 0 {
		return m
	}
	return pageRe.FindAllSubmatch(body, -1)
}

func extractMaxPage(body []byte) int {
	maxPage := 1
	for _, m := range pageNumbers(body) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

func hasNextPage(body []byte, current int) bool {
	for _, m := range pageNumbers(body) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > current {
			return true
		}
	}
	return false
}

var (
	metaDateRe    = regexp.MustCompile(`(?:itemprop="uploadDate"|name="Date")\s+content="([^"]+)"`)
	visibleDateRe = regexp.MustCompile(`class="label">Date:\s*</span>\s*<span class="value">([^<]+)</span>`)
	tagRe         = regexp.MustCompile(`href="[^"]*updates-tag/([^/]+)/\d+/[^"]*"[^>]*class="btn btn-ol-2[^"]*"[^>]*>\s*([^<]+)`)
)

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, delay time.Duration) (models.Scene, error) {
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return models.Scene{}, ctx.Err()
		}
	}

	body, err := s.fetchPage(ctx, ls.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", ls.id, err)
	}

	var date time.Time
	if m := metaDateRe.FindSubmatch(body); m != nil {
		if t, err := time.Parse(time.RFC3339, string(m[1])); err == nil {
			date = t.UTC()
		} else if t, err := time.Parse("2006-01-02T15:04:05+00:00", string(m[1])); err == nil {
			date = t.UTC()
		}
	}
	if date.IsZero() {
		if m := visibleDateRe.FindSubmatch(body); m != nil {
			date = parseVisibleDate(string(m[1]))
		}
	}

	desc := ""
	if v := parseutil.OpenGraph(body)["og:description"]; v != "" {
		desc = html.UnescapeString(v)
		desc = strings.TrimSuffix(desc, "…")
		desc = strings.TrimSpace(desc)
	}

	var tags []string
	tagMatches := tagRe.FindAllSubmatch(body, -1)
	seen := map[string]bool{}
	for _, m := range tagMatches {
		name := strings.TrimSpace(string(m[2]))
		if name != "" && !seen[name] {
			seen[name] = true
			tags = append(tags, name)
		}
	}

	title := ls.title
	if title == "" {
		if m := titleRe.FindSubmatch(body); m != nil {
			title = strings.TrimSpace(string(m[1]))
		}
	}

	return models.Scene{
		ID:          ls.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   s.cfg.SiteBase,
		Title:       title,
		URL:         ls.url,
		Date:        date,
		Description: desc,
		Duration:    ls.duration,
		Performers:  ls.performers,
		Tags:        tags,
		Thumbnail:   ls.thumb,
		Studio:      s.cfg.StudioName,
		ScrapedAt:   time.Now().UTC(),
	}, nil
}

func parseVisibleDate(s string) time.Time {
	cleaned := parseutil.StripOrdinalSuffix(strings.TrimSpace(s))
	if t, err := time.Parse("January 2, 2006", cleaned); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
