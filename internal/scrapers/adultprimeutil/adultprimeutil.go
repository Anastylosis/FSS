package adultprimeutil

import (
	"context"
	"encoding/json"
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

const defaultBase = "https://adultprime.com"

type SiteConfig struct {
	SiteID     string
	Slug       string // e.g. "Clubsweethearts" — used in ?website= and ?site= params
	StudioName string
}

type Scraper struct {
	client *http.Client
	base   string
	cfg    SiteConfig
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   defaultBase,
		cfg:    cfg,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"adultprime.com/studios/studio/" + s.cfg.Slug,
		"adultprime.com/studios/videos?website=" + s.cfg.Slug,
		"adultprime.com/studios/videos?website=" + s.cfg.Slug + "&niche={niche}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?adultprime\.com/studios/(?:studio|videos)`)

func (s *Scraper) MatchesURL(u string) bool {
	if !matchRe.MatchString(u) {
		return false
	}
	lower := strings.ToLower(u)
	slug := strings.ToLower(s.cfg.Slug)
	return strings.Contains(lower, "/studio/"+slug) ||
		strings.Contains(lower, "website="+slug)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var nicheRe = regexp.MustCompile(`[?&]niche=([^&#]+)`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	niche := ""
	if m := nicheRe.FindStringSubmatch(studioURL); m != nil {
		niche = m[1]
		scraper.Debugf(1, "%s: detected niche filter: %s", s.cfg.SiteID, niche)
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	type detailWork struct {
		listing listingItem
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)

		for page := 1; ; page++ {
			if ctx.Err() != nil {
				return
			}
			scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)

			url := fmt.Sprintf("%s/studios/videos?website=%s&page=%d", s.base, s.cfg.Slug, page)
			if niche != "" {
				url += "&niche=" + niche
			}

			body, err := s.fetchPage(ctx, url)
			if err != nil {
				select {
				case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
				case <-ctx.Done():
				}
				return
			}

			items := parseListingPage(body)
			if len(items) == 0 {
				return
			}

			if page == 1 {
				p := parseParams(body)
				total := p.Total
				if total == 0 {
					total = len(items)
				}
				scraper.Debugf(1, "%s: %d total scenes", s.cfg.SiteID, total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}

			for _, item := range items {
				if opts.KnownIDs[item.id] {
					scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
					select {
					case out <- scraper.StoppedEarly():
					case <-ctx.Done():
					}
					return
				}
				select {
				case work <- detailWork{listing: item}:
				case <-ctx.Done():
					return
				}
			}

			p := parseParams(body)
			if page*p.PageSize >= p.Total {
				return
			}

			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
}

type listingItem struct {
	id       string
	title    string
	date     string
	duration string
	thumb    string
}

type pageParams struct {
	Total    int    `json:"total"`
	PageSize int    `json:"pageSize"`
	Page     int    `json:"page"`
	Website  string `json:"website"`
}

var paramsRe = regexp.MustCompile(`var params = (\{[^}]+\})`)

func parseParams(body []byte) pageParams {
	m := paramsRe.FindSubmatch(body)
	if m == nil {
		return pageParams{}
	}
	var p pageParams
	_ = json.Unmarshal(m[1], &p)
	return p
}

var (
	blockRe     = regexp.MustCompile(`(?s)<div class="model-wrapper portal-video-wrapper">(.*?)</div>\s*</div>\s*</div>`)
	videoLinkRe = regexp.MustCompile(`href="/studios/video/(\d+)\?site=([^"]*)"`)
	titleListRe = regexp.MustCompile(`class="description-title[^"]*">([^<]+)</span>`)
	dateListRe  = regexp.MustCompile(`class="description-releasedate"><i class="fa fa-calendar"></i>\s*([^<]+)</span>`)
	durListRe   = regexp.MustCompile(`class="description-duration"><i class="fa fa-clock-o"></i>\s*([^<]+)</span>`)
	thumbListRe = regexp.MustCompile(`background-image:\s*url\(([^)]+)\)`)
)

func parseListingPage(body []byte) []listingItem {
	blocks := blockRe.FindAllSubmatch(body, -1)
	seen := map[string]bool{}
	var items []listingItem

	for _, b := range blocks {
		block := b[1]

		lm := videoLinkRe.FindSubmatch(block)
		if lm == nil {
			continue
		}
		id := string(lm[1])
		if seen[id] {
			continue
		}
		seen[id] = true

		item := listingItem{id: id}

		if m := titleListRe.FindSubmatch(block); m != nil {
			item.title = strings.TrimSpace(html.UnescapeString(string(m[1])))
		}
		if m := dateListRe.FindSubmatch(block); m != nil {
			item.date = strings.TrimSpace(string(m[1]))
		}
		if m := durListRe.FindSubmatch(block); m != nil {
			item.duration = strings.TrimSpace(string(m[1]))
		}
		if m := thumbListRe.FindSubmatch(block); m != nil {
			item.thumb = string(m[1])
		}

		items = append(items, item)
	}
	return items
}

var (
	detailTitleRe = regexp.MustCompile(`(?s)<h1 class="\s*update-info-title">\s*(.*?)\s*(?:Full video by|</h1>)`)
	detailDescRe  = regexp.MustCompile(`(?s)class="update-info-line ap-limited-description-text[^"]*">\s*(.*?)\s*</p>`)
	detailPerfRe  = regexp.MustCompile(`href="/pornstar/[^"]*"[^>]*>\s*([^<]+?)\s*</a>`)
	detailNicheRe = regexp.MustCompile(`href='/studios/videos\?niche=[^']*'\s*class='site-link'>([^<]+)</a>`)
	detailDateRe  = regexp.MustCompile(`class="description-releasedate"><i class="fa fa-calendar"></i>\s*(\d{2}-\d{2}-\d{4})</span>`)
	detailDurRe   = regexp.MustCompile(`class="description-duration[^"]*"><i class="fa fa-clock-o"></i>\s*(\d+)\s*min`)
)

func (s *Scraper) fetchDetail(ctx context.Context, item listingItem, delay time.Duration) (models.Scene, error) {
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return models.Scene{}, ctx.Err()
		}
	}

	url := fmt.Sprintf("%s/studios/video/%s?site=%s", s.base, item.id, s.cfg.Slug)
	body, err := s.fetchPage(ctx, url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", item.id, err)
	}

	scene := models.Scene{
		ID:        item.id,
		SiteID:    s.cfg.SiteID,
		StudioURL: fmt.Sprintf("%s/studios/studio/%s", s.base, s.cfg.Slug),
		Title:     item.title,
		URL:       fmt.Sprintf("%s/studios/video/%s?site=%s", s.base, item.id, strings.ToLower(s.cfg.Slug)),
		Thumbnail: item.thumb,
		Studio:    s.cfg.StudioName,
		ScrapedAt: time.Now().UTC(),
	}

	if m := detailTitleRe.FindSubmatch(body); m != nil {
		t := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if t != "" {
			scene.Title = t
		}
	}

	if m := detailDescRe.FindSubmatch(body); m != nil {
		scene.Description = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}

	perfMatches := detailPerfRe.FindAllSubmatch(body, -1)
	seen := map[string]bool{}
	for _, m := range perfMatches {
		name := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if name != "" && !seen[name] {
			seen[name] = true
			scene.Performers = append(scene.Performers, name)
		}
	}

	nicheMatches := detailNicheRe.FindAllSubmatch(body, -1)
	seenTag := map[string]bool{}
	for _, m := range nicheMatches {
		tag := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if tag != "" && !seenTag[tag] {
			seenTag[tag] = true
			scene.Tags = append(scene.Tags, tag)
		}
	}

	if m := detailDateRe.FindSubmatch(body); m != nil {
		if t, err := time.Parse("2-1-2006", string(m[1])); err == nil {
			scene.Date = t.UTC()
		}
	}
	if scene.Date.IsZero() && item.date != "" {
		if t, err := time.Parse("Jan 2, 2006", item.date); err == nil {
			scene.Date = t.UTC()
		}
	}

	if m := detailDurRe.FindSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(string(m[1]))
		scene.Duration = mins * 60
	}
	if scene.Duration == 0 {
		scene.Duration = parseutil.ParseDurationColon(item.duration)
	}

	return scene, nil
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
