package fpnutil

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
	"github.com/Anastylosis/FSS/scraper"
)

const pageSize = 20

type SiteConfig struct {
	SiteID     string
	Domain     string
	SiteBase   string
	StudioName string
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

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	type detailWork struct {
		listing listingScene
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

	kind, value := classifyURL(studioURL)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)
		skipped := 0
		for page := 1; ; page++ {
			if ctx.Err() != nil {
				return
			}
			if page > 1 && !scraper.Pace(ctx, opts.Delay) {
				return
			}
			scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)

			url := listingURL(s.cfg.SiteBase, kind, value, page)
			body, err := s.fetchPage(ctx, url)
			if err != nil {
				select {
				case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
				case <-ctx.Done():
				}
				return
			}

			scenes := ParseListingPage(body)
			if page == 1 {
				if kind == filterModel {
					select {
					case out <- scraper.Progress(len(scenes)):
					case <-ctx.Done():
						return
					}
				} else {
					maxPage := extractMaxPage(body)
					total := pageSize * maxPage
					if total == 0 {
						total = len(scenes)
					}
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
				if opts.KnownIDs[sc.ID] {
					// A model page is one un-paginated listing in no date order
					// at all — live, Jessica Starling's ran 2026-09, 2023-12,
					// 2022-10, 2024-11 — so a stop there would hide most of her
					// catalogue. It is already fetched, so skipping costs
					// nothing. The grid listings carry sort=most-recent and do
					// stop.
					if kind == filterModel {
						skipped++
						continue
					}
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

			if kind == filterModel || !hasNextPage(body, page) {
				if skipped > 0 {
					scraper.Debugf(1, "%s: skipped %d already-stored scene(s)", s.cfg.SiteID, skipped)
					select {
					case out <- scraper.StoppedEarly():
					case <-ctx.Done():
					}
				}
				return
			}
		}
	}()

	wg.Wait()
}

type filterKind int

const (
	filterAll filterKind = iota
	filterModel
	filterCategory
)

var (
	modelURLRe    = regexp.MustCompile(`/models/([^/?#]+)\.html`)
	categoryURLRe = regexp.MustCompile(`/(?:porn-categories|channels)/([^/?#]+)`)
)

func classifyURL(u string) (filterKind, string) {
	if m := modelURLRe.FindStringSubmatch(u); m != nil {
		return filterModel, m[1]
	}
	if m := categoryURLRe.FindStringSubmatch(u); m != nil {
		slug := m[1]
		if slug == "movies" || slug == "" {
			return filterAll, ""
		}
		return filterCategory, slug
	}
	return filterAll, ""
}

func listingURL(siteBase string, kind filterKind, value string, page int) string {
	switch kind {
	case filterModel:
		return fmt.Sprintf("%s/models/%s.html", siteBase, value)
	case filterCategory:
		return fmt.Sprintf("%s/porn-categories/%s/?page=%d&sort=most-recent", siteBase, value, page)
	default:
		return fmt.Sprintf("%s/porn-categories/movies/?page=%d&sort=most-recent", siteBase, page)
	}
}

type listingScene struct {
	ID         string
	Slug       string
	Title      string
	Thumb      string
	Duration   int
	Performers []string
}

var (
	// The grid listings carry the scene id in data-setid. The model page was
	// rebuilt as a "swimlane" whose cards leave data-setid empty and put the id
	// in data-scene-id instead, so every model page parsed to zero cards.
	cardRe      = regexp.MustCompile(`data-(?:setid|scene-id)="(\d+)"`)
	titleLinkRe = regexp.MustCompile(`<a[^>]*title="([^"]*)"[^>]*href="/trailers/([^"]+)"`)
	durationRe  = regexp.MustCompile(`class="video-data">(\d+)\s*min</div>`)
	thumbRe     = regexp.MustCompile(`data-src="(https://c[a-f0-9]+\.mjedge\.net/content/contentthumbs/[^"]+)"`)
	performerRe = regexp.MustCompile(`<a[^>]*\shref="/models/[^"]+"[^>]*>([^<]+)</a>`)
)

func ParseListingPage(body []byte) []listingScene {
	s := string(body)
	cardLocs := cardRe.FindAllStringSubmatchIndex(s, -1)
	var scenes []listingScene

	for i, loc := range cardLocs {
		start := loc[0]
		end := len(s)
		if i+1 < len(cardLocs) {
			end = cardLocs[i+1][0]
		}
		block := s[start:end]

		id := s[loc[2]:loc[3]]

		titleM := titleLinkRe.FindStringSubmatch(block)
		if titleM == nil {
			continue
		}
		title := html.UnescapeString(titleM[1])
		slug := titleM[2]

		var dur int
		if m := durationRe.FindStringSubmatch(block); m != nil {
			dur, _ = strconv.Atoi(m[1])
			dur *= 60
		}

		var thumb string
		if m := thumbRe.FindStringSubmatch(block); m != nil {
			thumb = m[1]
		}

		var performers []string
		seen := map[string]bool{}
		for _, m := range performerRe.FindAllStringSubmatch(block, -1) {
			name := strings.TrimSpace(m[1])
			if name != "" && !seen[name] {
				seen[name] = true
				performers = append(performers, name)
			}
		}

		scenes = append(scenes, listingScene{
			ID:         id,
			Slug:       slug,
			Title:      title,
			Thumb:      thumb,
			Duration:   dur,
			Performers: performers,
		})
	}
	return scenes
}

var pageNumRe = regexp.MustCompile(`[?&]page=(\d+)`)

func extractMaxPage(body []byte) int {
	matches := pageNumRe.FindAllSubmatch(body, -1)
	maxPage := 1
	for _, m := range matches {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

func hasNextPage(body []byte, current int) bool {
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > current {
			return true
		}
	}
	return false
}

var (
	detailDescRe = regexp.MustCompile(`<p\s+id="description"[^>]*>\s*([\s\S]*?)\s*</p>`)
	detailDateRe = regexp.MustCompile(`Date Added:\s*</label>\s*(\d{4}-\d{2}-\d{2})`)
	detailTagRe  = regexp.MustCompile(`href="/porn-categories/([^/]+)/"[^>]*>([^<]+)</a>`)
)

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	url := fmt.Sprintf("%s/trailers/%s/", s.cfg.SiteBase, ls.Slug)
	body, err := s.fetchPage(ctx, url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", ls.ID, err)
	}

	var date time.Time
	if m := detailDateRe.FindSubmatch(body); m != nil {
		if t, err := time.Parse("2006-01-02", string(m[1])); err == nil {
			date = t.UTC()
		}
	}

	var desc string
	if m := detailDescRe.FindSubmatch(body); m != nil {
		desc = strings.TrimSpace(string(m[1]))
		desc = html.UnescapeString(desc)
	}

	var tags []string
	seen := map[string]bool{}
	for _, m := range detailTagRe.FindAllSubmatch(body, -1) {
		name := strings.TrimSpace(string(m[2]))
		if name != "" && !seen[name] {
			seen[name] = true
			tags = append(tags, name)
		}
	}

	performers := ls.Performers
	if len(performers) == 0 {
		performers = parseDetailPerformers(body)
	}

	return models.Scene{
		ID:          ls.ID,
		SiteID:      s.cfg.SiteID,
		StudioURL:   s.cfg.SiteBase,
		Title:       ls.Title,
		URL:         url,
		Date:        date,
		Description: desc,
		Duration:    ls.Duration,
		Performers:  performers,
		Tags:        tags,
		Thumbnail:   ls.Thumb,
		Studio:      s.cfg.StudioName,
		ScrapedAt:   time.Now().UTC(),
	}, nil
}

var (
	detailPerformerBlockRe = regexp.MustCompile(`(?s)<a\s+href="/models/[^"]+\.html">(.*?)</a>`)
	stripTagsRe            = regexp.MustCompile(`<[^>]+>`)
)

func parseDetailPerformers(body []byte) []string {
	var performers []string
	seen := map[string]bool{}
	for _, m := range detailPerformerBlockRe.FindAllSubmatch(body, -1) {
		text := stripTagsRe.ReplaceAll(m[1], nil)
		name := strings.TrimSpace(string(text))
		if name != "" && !seen[name] {
			seen[name] = true
			performers = append(performers, name)
		}
	}
	return performers
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
