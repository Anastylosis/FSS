package apovstory

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

const defaultSiteBase = "https://apovstory.com"

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

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "apovstory" }

func (s *Scraper) Patterns() []string {
	return []string{
		"apovstory.com",
		"apovstory.com/models/{name}.html",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?apovstory\.com(?:/|$)`)
	modelRe = regexp.MustCompile(`/models/[^/]+\.html`)
)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listEntry struct {
	id         string
	title      string
	url        string
	thumbnail  string
	preview    string
	performers []string
	duration   int
	date       time.Time
	rating     float64
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	work := make(chan listEntry, opts.Workers)
	var wg sync.WaitGroup

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, studioURL, entry)
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

	if modelRe.MatchString(studioURL) {
		s.produceModel(ctx, studioURL, opts, out, work)
	} else {
		s.produceListing(ctx, studioURL, opts, out, work)
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) produceModel(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listEntry) {
	body, err := s.fetchBody(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	entries := parseEntries(body, s.siteBase)
	if len(entries) > 0 {
		select {
		case out <- scraper.Progress(len(entries)):
		case <-ctx.Done():
			return
		}
	}
	for _, e := range entries {
		if opts.KnownIDs[e.id] {
			continue
		}
		select {
		case work <- e:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) produceListing(ctx context.Context, _ string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listEntry) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			// `return`, not `break`: break leaves the select, not the for
			// loop, so a cancellation during the delay fell through and
			// fetched the page anyway — producing a spurious
			// "page N: context canceled" error on every cancelled scrape.
			return
		}
		scraper.Debugf(1, "apovstory: fetching page %d", page)

		entries, err := s.fetchPage(ctx, page)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		if len(entries) == 0 {
			break
		}

		if page == 1 {
			select {
			case out <- scraper.Progress(estimateTotal(len(entries))):
			case <-ctx.Done():
				return
			}
		}

		cancelled := false
		hitKnown := false
		for _, e := range entries {
			if opts.KnownIDs[e.id] {
				hitKnown = true
				break
			}
			select {
			case work <- e:
			case <-ctx.Done():
				cancelled = true
			}
			if cancelled {
				break
			}
		}
		if cancelled || hitKnown {
			if hitKnown {
				scraper.Debugf(1, "apovstory: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
			}
			break
		}
	}
}

func estimateTotal(firstPageCount int) int {
	return firstPageCount * 19
}

var (
	latestSectionRe = regexp.MustCompile(`(?s)Latest Updates(.*?)Most Popular Updates`)
	videoBlockRe    = regexp.MustCompile(`(?s)<div class="videoBlock" data-setid="(\d+)">(.*?)</div><!--//updateDetails-->\s*</div>`)
	sceneLinkRe     = regexp.MustCompile(`href="(https?://[^"]*?/trailers/[^"]+\.html)"`)
	sceneTitleRe    = regexp.MustCompile(`title="([^"]+)"`)
	thumbURLRe      = regexp.MustCompile(`src="(/content/[^"]+)"`)
	previewRe       = regexp.MustCompile(`src='(/videothumbs/[^']+)'`)
	modelsBlockRe   = regexp.MustCompile(`(?s)updateDetails_models">(.*?)</div>`)
	performerRe     = regexp.MustCompile(`>([^<]+)</a>`)
	ratingDurRe     = regexp.MustCompile(`star-o"></i>\s*([\d.]+)\s*\|\s*([\d:]+)`)
	dateRe          = regexp.MustCompile(`(?s)updateDetails_date">\s*([^<]+)`)
)

func (s *Scraper) fetchPage(ctx context.Context, page int) ([]listEntry, error) {
	pageURL := fmt.Sprintf("%s/updates/page_%d.html", s.siteBase, page)
	body, err := s.fetchBody(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	return parseEntries(body, s.siteBase), nil
}

func (s *Scraper) fetchBody(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func parseEntries(body []byte, siteBase string) []listEntry {
	section := body
	if m := latestSectionRe.FindSubmatch(body); m != nil {
		section = m[1]
	}

	blocks := videoBlockRe.FindAllSubmatch(section, -1)
	entries := make([]listEntry, 0, len(blocks))

	for _, block := range blocks {
		setID := string(block[1])
		content := block[2]

		entry := listEntry{id: setID}

		if m := sceneLinkRe.FindSubmatch(content); m != nil {
			entry.url = string(m[1])
		}
		if m := sceneTitleRe.FindSubmatch(content); m != nil {
			entry.title = html.UnescapeString(string(m[1]))
		}
		if m := thumbURLRe.FindSubmatch(content); m != nil {
			entry.thumbnail = siteBase + string(m[1])
		}
		if m := previewRe.FindSubmatch(content); m != nil {
			entry.preview = siteBase + string(m[1])
		}
		if m := modelsBlockRe.FindSubmatch(content); m != nil {
			for _, pm := range performerRe.FindAllSubmatch(m[1], -1) {
				name := strings.TrimSpace(string(pm[1]))
				if name != "" {
					entry.performers = append(entry.performers, name)
				}
			}
		}
		if m := ratingDurRe.FindSubmatch(content); m != nil {
			entry.rating, _ = strconv.ParseFloat(string(m[1]), 64)
			entry.duration = parseutil.ParseDurationColon(string(m[2]))
		}
		if m := dateRe.FindSubmatch(content); m != nil {
			entry.date = parseDate(strings.TrimSpace(string(m[1])))
		}

		entries = append(entries, entry)
	}

	return entries
}

var (
	categoryLinkRe = regexp.MustCompile(`categories/\w+_1_d\.html">([^<]+)</a>`)
)

func (s *Scraper) fetchDetail(ctx context.Context, studioURL string, entry listEntry) (models.Scene, error) {
	now := time.Now().UTC()

	scene := models.Scene{
		ID:         entry.id,
		SiteID:     "apovstory",
		StudioURL:  studioURL,
		Title:      entry.title,
		URL:        entry.url,
		Thumbnail:  entry.thumbnail,
		Preview:    entry.preview,
		Performers: entry.performers,
		Duration:   entry.duration,
		Date:       entry.date,
		Studio:     "A POV Story",
		ScrapedAt:  now,
	}

	if entry.url == "" {
		return scene, nil
	}

	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     entry.url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return scene, nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return scene, nil
	}

	og := parseutil.OpenGraph(body)
	if v := og["og:description"]; v != "" {
		scene.Description = html.UnescapeString(v)
	}

	for _, cm := range categoryLinkRe.FindAllSubmatch(body, -1) {
		scene.Tags = append(scene.Tags, strings.TrimSpace(string(cm[1])))
	}

	return scene, nil
}

func parseDate(s string) time.Time {
	t, err := time.Parse("January 2, 2006", s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
