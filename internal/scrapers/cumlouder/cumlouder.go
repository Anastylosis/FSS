package cumlouder

import (
	"context"
	"fmt"
	"html"
	"math"
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

const siteBase = "https://www.cumlouder.com"

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "cumlouder" }

func (s *Scraper) Patterns() []string {
	return []string{
		"cumlouder.com/site/{slug}",
		"cumlouder.com/girl/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?cumlouder\.com/(?:site|girl)/[^/]+`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardRe       = regexp.MustCompile(`(?s)<a\s+class="muestra-escena"\s+href="([^"]+)"[^>]*>(.*?)</a>`)
	imgSrcRe     = regexp.MustCompile(`data-src="([^"]+)"`)
	imgAltRe     = regexp.MustCompile(`alt="([^"]*)"`)
	viewsRe      = regexp.MustCompile(`ico-vistas[^<]*</span>\s*(\d[\d,]*)\s*views`)
	relDateRe    = regexp.MustCompile(`ico-fecha[^<]*</span>\s*([^<]+)<`)
	durationRe   = regexp.MustCompile(`ico-minutos[^<]*</span>\s*([^<]+)<`)
	totalRe      = regexp.MustCompile(`ico-videos[^<]*</span>\s*(\d[\d,]*)\s*Videos`)
	titleH2Re    = regexp.MustCompile(`<h2>\s*(?:<span[^>]*>[^<]*</span>\s*)?([^<]+)</h2>`)
	detailDescRe = regexp.MustCompile(`(?is)<p>\s*<strong>Description:</strong>\s*(.*?)</p>`)
	detailTagRe  = regexp.MustCompile(`<a\s+class="tag-link"[^>]+>([^<]+)</a>`)
	detailPerfRe = regexp.MustCompile(`<a\s+class="pornstar-link"[^>]+>([^<]+)</a>`)
	detailDurRe  = regexp.MustCompile(`(?s)class="duracion"[^>]*>.*?(\d+:\d+\s*[mh])`)
	detailViewRe = regexp.MustCompile(`Views:\s*(\d[\d,]*)`)
	posterRe     = regexp.MustCompile(`poster='([^']+)'`)
	approxDateRe = regexp.MustCompile(`(\d+)\s+(year|month|week|day|hour|minute)s?\s+ago`)
)

type listItem struct {
	slug     string
	url      string
	title    string
	thumb    string
	views    int
	duration int
	relDate  string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	studioURL = strings.TrimRight(studioURL, "/")

	isGirl := strings.Contains(studioURL, "/girl/")

	body, err := s.fetch(ctx, studioURL+"/")
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("first page: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	items := parseCards(body)

	var totalPages int
	if !isGirl {
		total := parseTotal(body)
		if total > 0 {
			totalPages = int(math.Ceil(float64(total) / 30.0))
		}
	}

	for page := 2; page <= totalPages; page++ {
		if ctx.Err() != nil {
			return
		}
		if opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return
			}
		}
		scraper.Debugf(1, "cumlouder: fetching page %d", page)

		pageBody, err := s.fetch(ctx, fmt.Sprintf("%s/%d/", studioURL, page))
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
				return
			}
			continue
		}
		pageItems := parseCards(pageBody)
		if len(pageItems) == 0 {
			break
		}
		items = append(items, pageItems...)
	}

	items = dedup(items)

	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	type detailResult struct {
		scene models.Scene
		err   error
	}

	work := make(chan int, len(items))
	for i := range items {
		work <- i
	}
	close(work)

	results := make(chan detailResult, len(items))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				if ctx.Err() != nil {
					return
				}
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				scene, err := s.fetchDetail(ctx, items[i], studioURL)
				results <- detailResult{scene: scene, err: err}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		if ctx.Err() != nil {
			return
		}
		if r.err != nil {
			select {
			case out <- scraper.Error(r.err):
			case <-ctx.Done():
				return
			}
			continue
		}
		select {
		case out <- scraper.Scene(r.scene):
		case <-ctx.Done():
			return
		}
	}
}

func parseCards(body []byte) []listItem {
	matches := cardRe.FindAllSubmatch(body, -1)
	var items []listItem
	for _, m := range matches {
		href := string(m[1])
		card := m[2]

		slug := sceneSlug(href)
		if slug == "" {
			continue
		}

		url := href
		if !strings.HasPrefix(url, "http") {
			url = siteBase + url
		}

		item := listItem{slug: slug, url: url}

		if sm := imgAltRe.FindSubmatch(card); sm != nil {
			item.title = strings.TrimSpace(string(sm[1]))
		}
		if sm := titleH2Re.FindSubmatch(card); sm != nil {
			item.title = strings.TrimSpace(string(sm[1]))
		}
		if sm := imgSrcRe.FindSubmatch(card); sm != nil {
			item.thumb = string(sm[1])
		}
		if sm := viewsRe.FindSubmatch(card); sm != nil {
			item.views = parseIntCommas(string(sm[1]))
		}
		if sm := durationRe.FindSubmatch(card); sm != nil {
			item.duration = parseDuration(strings.TrimSpace(string(sm[1])))
		}
		if sm := relDateRe.FindSubmatch(card); sm != nil {
			item.relDate = strings.TrimSpace(string(sm[1]))
		}

		items = append(items, item)
	}
	return items
}

func (s *Scraper) fetchDetail(ctx context.Context, item listItem, studioURL string) (models.Scene, error) {
	now := time.Now().UTC()
	scene := models.Scene{
		ID:        item.slug,
		SiteID:    "cumlouder",
		StudioURL: studioURL,
		Title:     item.title,
		URL:       item.url,
		Thumbnail: item.thumb,
		Views:     item.views,
		Duration:  item.duration,
		ScrapedAt: now,
	}

	if item.relDate != "" {
		scene.Date = approxDate(item.relDate, now)
	}

	body, err := s.fetch(ctx, item.url)
	if err != nil {
		return scene, nil
	}

	for _, m := range detailTagRe.FindAllSubmatch(body, -1) {
		scene.Tags = append(scene.Tags, html.UnescapeString(strings.TrimSpace(string(m[1]))))
	}

	for _, m := range detailPerfRe.FindAllSubmatch(body, -1) {
		scene.Performers = append(scene.Performers, html.UnescapeString(strings.TrimSpace(string(m[1]))))
	}

	if m := detailDescRe.FindSubmatch(body); m != nil {
		scene.Description = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := detailDurRe.FindSubmatch(body); m != nil {
		scene.Duration = parseDuration(string(m[1]))
	}

	if m := detailViewRe.FindSubmatch(body); m != nil {
		scene.Views = parseIntCommas(string(m[1]))
	}

	if scene.Thumbnail == "" {
		if m := posterRe.FindSubmatch(body); m != nil {
			scene.Thumbnail = string(m[1])
		}
	}

	return scene, nil
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	h["Cookie"] = "parentDni=0"

	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: h,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func parseTotal(body []byte) int {
	m := totalRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	return parseIntCommas(string(m[1]))
}

func sceneSlug(href string) string {
	href = strings.TrimRight(href, "/")
	if i := strings.LastIndex(href, "/porn-video/"); i >= 0 {
		return href[i+len("/porn-video/"):]
	}
	return ""
}

func dedup(items []listItem) []listItem {
	seen := make(map[string]bool, len(items))
	out := make([]listItem, 0, len(items))
	for _, item := range items {
		if seen[item.slug] {
			continue
		}
		seen[item.slug] = true
		out = append(out, item)
	}
	return out
}

func parseIntCommas(s string) int {
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, ".", "")
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func parseDuration(s string) int {
	s = strings.TrimSpace(s)
	isHour := strings.HasSuffix(s, "h")
	s = strings.TrimSuffix(s, " m")
	s = strings.TrimSuffix(s, " h")
	s = strings.TrimSuffix(s, "m")
	s = strings.TrimSuffix(s, "h")
	s = strings.TrimSpace(s)

	parts := strings.Split(s, ":")
	switch len(parts) {
	case 2:
		a, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		b, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		if isHour {
			return a*3600 + b*60
		}
		return a*60 + b
	case 3:
		h, _ := strconv.Atoi(parts[0])
		m, _ := strconv.Atoi(parts[1])
		sec, _ := strconv.Atoi(parts[2])
		return h*3600 + m*60 + sec
	}
	return 0
}

func approxDate(rel string, now time.Time) time.Time {
	rel = strings.TrimSpace(strings.ToLower(rel))
	m := approxDateRe.FindStringSubmatch(rel)
	if m == nil {
		return time.Time{}
	}
	n, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "year":
		return now.AddDate(-n, 0, 0)
	case "month":
		return now.AddDate(0, -n, 0)
	case "week":
		return now.AddDate(0, 0, -n*7)
	case "day":
		return now.AddDate(0, 0, -n)
	case "hour":
		return now.Add(-time.Duration(n) * time.Hour)
	case "minute":
		return now.Add(-time.Duration(n) * time.Minute)
	}
	return time.Time{}
}
