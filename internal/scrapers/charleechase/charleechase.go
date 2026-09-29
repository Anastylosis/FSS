package charleechase

import (
	"context"
	"fmt"
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
	baseURL = "https://charleechaselive.com"
	siteID  = "charleechase"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?charleechaselive\.com(?:/(?:sd3\.php\?show=recent_video_updates|videos(?:/page/\d+)?))?/?$`)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   baseURL,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string               { return siteID }
func (s *Scraper) Patterns() []string       { return []string{"charleechaselive.com/videos"} }
func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, "charleechase", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + "/videos/page/" + strconv.Itoa(page)
		items, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		scenes := make([]models.Scene, 0, len(items))
		for _, item := range items {
			// Stop before detail-fetching known IDs; include a stub
			// scene so Paginate can see the ID and trigger early-stop.
			if opts.KnownIDs[item.id] {
				scenes = append(scenes, models.Scene{ID: item.id})
				break
			}

			if !scraper.Pace(ctx, opts.Delay) {
				return scraper.PageResult{}, ctx.Err()
			}

			detail, detailErr := s.fetchDetail(ctx, item.href)
			if detailErr != nil {
				select {
				case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.id, detailErr)):
				case <-ctx.Done():
				}
				detail = &detailData{}
			}

			scenes = append(scenes, toScene(studioURL, item, detail, now))
		}
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// --- listing ---

var (
	videoAreaRe = regexp.MustCompile(`(?s)<div class="videoarea clear">(.*?)</div>\s*</div>\s*</div>`)
	titleRe     = regexp.MustCompile(`<h3><a href="(videos/(\d+)/[^"]+)">([^<]+)</a></h3>`)
	dateRe      = regexp.MustCompile(`<p class="date">([^<]+)</p>`)
	thumbRe     = regexp.MustCompile(`<img src="([^"]*(?:thumb_1\.jpg|thumb_\d+\.jpg))"`)
	sectionRe   = regexp.MustCompile(`<h4><a[^>]*>Section:\s*([^<]+)</a></h4>`)
	featuringRe = regexp.MustCompile(`<h5>Featuring:\s*([^<]+)</h5>`)
	descRe      = regexp.MustCompile(`(?s)<div class="video_details[^"]*">\s*(?:<h4[^>]*>.*?</h4>\s*)?(?:<h5>[^<]*</h5>\s*)?<p>(.+?)(?:<br>|<br\s*/>)`)
	priceRe     = regexp.MustCompile(`Download this clip for \$(\d+\.\d+)`)
)

type listItem struct {
	id          string
	href        string
	title       string
	date        time.Time
	thumbnail   string
	category    string
	performers  []string
	description string
	price       float64
}

func (s *Scraper) fetchListing(ctx context.Context, rawURL string) ([]listItem, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading page: %w", err)
	}

	return parseListItems(body), nil
}

func parseListItems(body []byte) []listItem {
	blocks := videoAreaRe.FindAllSubmatch(body, -1)
	items := make([]listItem, 0, len(blocks))
	for _, block := range blocks {
		if item, ok := parseListItem(block[1]); ok {
			items = append(items, item)
		}
	}
	return items
}

func parseListItem(block []byte) (listItem, bool) {
	m := titleRe.FindSubmatch(block)
	if m == nil {
		return listItem{}, false
	}
	item := listItem{
		href:  string(m[1]),
		id:    string(m[2]),
		title: strings.TrimSpace(string(m[3])),
	}

	if md := dateRe.FindSubmatch(block); md != nil {
		item.date = parseDate(string(md[1]))
	}

	if mt := thumbRe.FindSubmatch(block); mt != nil {
		item.thumbnail = string(mt[1])
	}

	if ms := sectionRe.FindSubmatch(block); ms != nil {
		item.category = strings.TrimSpace(string(ms[1]))
	}

	if mf := featuringRe.FindSubmatch(block); mf != nil {
		item.performers = splitPerformers(string(mf[1]))
	}

	if mdesc := descRe.FindSubmatch(block); mdesc != nil {
		item.description = strings.TrimSpace(string(mdesc[1]))
	}

	if mp := priceRe.FindSubmatch(block); mp != nil {
		item.price, _ = strconv.ParseFloat(string(mp[1]), 64)
	}

	return item, true
}

func splitPerformers(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- detail page ---

var (
	detailTagsRe     = regexp.MustCompile(`<h4[^>]*class="customhcolor"[^>]*>([^<]+)</h4>`)
	detailDurationRe = regexp.MustCompile(`video duration <strong>(\d{2}:\d{2}:\d{2})</strong>`)
)

type detailData struct {
	tags     []string
	duration int
}

func (s *Scraper) fetchDetail(ctx context.Context, path string) (*detailData, error) {
	rawURL := s.base + "/" + path

	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading detail: %w", err)
	}

	return parseDetail(body), nil
}

func parseDetail(body []byte) *detailData {
	d := &detailData{}

	if m := detailTagsRe.FindSubmatch(body); m != nil {
		raw := strings.TrimSpace(string(m[1]))
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				d.tags = append(d.tags, t)
			}
		}
	}

	if m := detailDurationRe.FindSubmatch(body); m != nil {
		d.duration = parseutil.ParseDurationColon(string(m[1]))
	}

	return d
}

// --- conversion ---

func toScene(studioURL string, item listItem, detail *detailData, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          item.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         baseURL + "/" + item.href,
		Date:        item.date,
		Description: item.description,
		Thumbnail:   item.thumbnail,
		Performers:  item.performers,
		Categories:  []string{item.category},
		Studio:      "Charlee Chase",
		ScrapedAt:   now,
	}

	if item.category == "" {
		sc.Categories = nil
	}

	if detail != nil {
		sc.Tags = detail.tags
		sc.Duration = detail.duration
	}

	if item.price > 0 {
		sc.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: item.price,
		})
	}

	return sc
}

// --- date parsing ---

var months = map[string]time.Month{
	"january": time.January, "february": time.February, "march": time.March,
	"april": time.April, "may": time.May, "june": time.June,
	"july": time.July, "august": time.August, "september": time.September,
	"october": time.October, "november": time.November, "december": time.December,
}

var dateParseRe = regexp.MustCompile(`(?i)(\w+)\s+(\d+)\w*\s+(\d{4})`)

func parseDate(s string) time.Time {
	m := dateParseRe.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}
	}
	month, ok := months[strings.ToLower(m[1])]
	if !ok {
		return time.Time{}
	}
	day, _ := strconv.Atoi(m[2])
	year, _ := strconv.Atoi(m[3])
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
