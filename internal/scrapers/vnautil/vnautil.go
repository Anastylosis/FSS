package vnautil

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

type SiteConfig struct {
	SiteID      string
	Domain      string
	Studio      string
	VideoPrefix string // "videos", "videoset", "milf-videos"
	NeedsWWW    bool   // true if the domain requires www. prefix
}

type Scraper struct {
	cfg        SiteConfig
	client     *http.Client
	base       string
	hrefRe     *regexp.Regexp
	pageLinkRe *regexp.Regexp
	matchRe    *regexp.Regexp
}

func New(cfg SiteConfig) *Scraper {
	host := cfg.Domain
	if cfg.NeedsWWW {
		host = "www." + host
	}
	return &Scraper{
		cfg:        cfg,
		client:     httpx.NewClient(30 * time.Second),
		base:       "https://" + host,
		hrefRe:     buildHrefRe(cfg.VideoPrefix),
		pageLinkRe: buildPageLinkRe(cfg.VideoPrefix),
		matchRe:    BuildMatchRe(cfg.Domain, cfg.VideoPrefix),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func NewWithBase(cfg SiteConfig, base string, client *http.Client) *Scraper {
	return &Scraper{
		cfg:        cfg,
		client:     client,
		base:       base,
		hrefRe:     buildHrefRe(cfg.VideoPrefix),
		pageLinkRe: buildPageLinkRe(cfg.VideoPrefix),
		matchRe:    BuildMatchRe(cfg.Domain, cfg.VideoPrefix),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{s.cfg.Domain + "/" + s.cfg.VideoPrefix}
}

func BuildMatchRe(domain, prefix string) *regexp.Regexp {
	escaped := strings.ReplaceAll(domain, ".", `\.`)
	pattern := `^https?://(?:www\.)?` + escaped + `(?:/(?:sd3\.php\?show=recent_video_updates|` + prefix + `(?:/page/\d+)?))?/?$`
	return regexp.MustCompile(pattern)
}

func (s *Scraper) MatchesURL(u string) bool {
	return s.matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/%s/page/%d", s.base, s.cfg.VideoPrefix, page)

		body, err := fetchPage(ctx, s.client, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := s.parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			total = s.estimateTotal(body, len(items))
		}

		hasInlineTags := hasInlineTagsOrDuration(body)
		if !hasInlineTags {
			for i, item := range items {
				if ctx.Err() != nil {
					return scraper.PageResult{}, ctx.Err()
				}
				if i > 0 && opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return scraper.PageResult{}, ctx.Err()
					}
				}

				detail, err := s.fetchDetail(ctx, item.Href)
				if err != nil {
					detail = &DetailData{}
				}
				if len(detail.Tags) > 0 {
					items[i].Tags = detail.Tags
				}
				if detail.Duration > 0 {
					items[i].Duration = detail.Duration
				}
				if len(items[i].Performers) == 0 {
					items[i].Performers = detail.Performers
				}
				if items[i].Description == "" {
					items[i].Description = detail.Description
				}
			}
		}

		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = ToScene(s.cfg, s.base, studioURL, item, now)
		}

		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   !s.hasNextPage(body, page),
		}, nil
	})
}

// --- listing parsing (generic, works across all VNA template variants) ---

type ListItem struct {
	ID          string
	Href        string
	Title       string
	Date        time.Time
	Thumbnail   string
	Category    string
	Performers  []string
	Description string
	Price       float64
	Tags        []string
	Duration    int
}

var (
	// Title: <h3> or <h4 class="video-title"> or <p class="member-block_content__title">
	titleH3Re    = regexp.MustCompile(`<h3[^>]*>\s*(?:<a[^>]*>)?\s*([^<]+?)\s*(?:</a>)?\s*</h3>`)
	titleH4Re    = regexp.MustCompile(`<h4[^>]*class="video-title"[^>]*>([^<]+)</h4>`)
	titleBlockRe = regexp.MustCompile(`(?s)<p class="member-block_content__title">\s*(.+?)\s*</p>`)

	// Date: ordinal date in various containers
	dateRe = regexp.MustCompile(`(?:Date:|Posted|class="date"|class="posted"|class="added-date").*?([A-Z][a-z]+\s+\d+\w*\s+\d{4})`)
	// Fallback: bare ordinal date
	dateFallbackRe = regexp.MustCompile(`([A-Z][a-z]+\s+\d{1,2}(?:st|nd|rd|th)\s+\d{4})`)

	// Section/Category
	sectionRe = regexp.MustCompile(`Section[:\s]*(?:<[^>]*>)*\s*([^<]+?)(?:</|<br|\s+l\s)`)

	// Performers
	perfRe = regexp.MustCompile(`(?:Featuring|Starring|Stars Appearing|Models)[:\s]*(?:<[^>]*>)*\s*([^<]+)`)

	// Description: longest <p> that contains 30+ chars of text
	descRe = regexp.MustCompile(`<p[^>]*>([^<]{30,}?)(?:</p>|<br>|<br\s*/>)`)
	// Fallback for some templates where desc is not in <p> but after <span>The Story:</span>
	descStoryRe = regexp.MustCompile(`(?s)The Story:</span>\s*(.+?)(?:<br>|<span)`)

	// Price
	priceRe = regexp.MustCompile(`Download this clip for \$(\d+\.\d+)`)

	// Thumbnail
	thumbRe = regexp.MustCompile(`<img[^>]*src="(sd3\.php\?show=file&path=/videos/\d+/thumb_1\.jpg)"`)

	// Inline tags (Template C only)
	inlineTagsRe = regexp.MustCompile(`<p>Tags:\s*([^<]+)</p>`)

	// Inline duration
	inlineDurRe = regexp.MustCompile(`Duration:\s*<strong>(\d{2}:\d{2}:\d{2})</strong>`)
)

func hasInlineTagsOrDuration(body []byte) bool {
	return inlineTagsRe.Match(body) && inlineDurRe.Match(body)
}

func (s *Scraper) parseListing(body []byte) []ListItem {
	return parseListingWithRe(body, s.hrefRe)
}

func ParseListing(body []byte, videoPrefix string) []ListItem {
	return parseListingWithRe(body, buildHrefRe(videoPrefix))
}

func parseListingWithRe(body []byte, hrefRe *regexp.Regexp) []ListItem {
	page := string(body)

	allMatches := hrefRe.FindAllStringSubmatchIndex(page, -1)
	if len(allMatches) == 0 {
		return nil
	}

	type scenePos struct {
		id   string
		href string
		pos  int
	}
	seen := make(map[string]bool)
	var scenes []scenePos
	for _, m := range allMatches {
		id := page[m[4]:m[5]]
		if seen[id] {
			continue
		}
		seen[id] = true
		scenes = append(scenes, scenePos{
			id:   id,
			href: page[m[2]:m[3]],
			pos:  m[0],
		})
	}

	items := make([]ListItem, 0, len(scenes))
	for i, sc := range scenes {
		start := sc.pos - 300
		if start < 0 {
			start = 0
		}
		if i > 0 && start < scenes[i-1].pos {
			start = scenes[i-1].pos
		}
		end := len(page)
		if i+1 < len(scenes) {
			end = scenes[i+1].pos
		}
		block := page[start:end]

		item := ListItem{
			ID:   sc.id,
			Href: sc.href,
		}

		item.Title = unescapeTrim(extractTitle(block))
		if item.Title == "" {
			item.Title = titleFromSlug(sc.href)
		}

		if md := dateRe.FindStringSubmatch(block); md != nil {
			item.Date = ParseDate(md[1])
		} else if md := dateFallbackRe.FindStringSubmatch(block); md != nil {
			item.Date = ParseDate(md[1])
		}

		if mt := thumbRe.FindStringSubmatch(block); mt != nil {
			item.Thumbnail = mt[1]
		}

		if ms := sectionRe.FindStringSubmatch(block); ms != nil {
			cat := strings.TrimSpace(ms[1])
			if len(cat) >= 3 && (!strings.Contains(strings.ToLower(cat), "video") || len(cat) > 10) {
				item.Category = cat
			}
		}

		if mp := perfRe.FindStringSubmatch(block); mp != nil {
			raw := strings.TrimSpace(mp[1])
			if idx := strings.Index(raw, "|"); idx >= 0 {
				raw = strings.TrimSpace(raw[:idx])
			}
			if idx := strings.Index(raw, "Duration:"); idx >= 0 {
				raw = strings.TrimSpace(raw[:idx])
			}
			if raw != "" {
				item.Performers = splitPerformers(raw)
			}
		}

		item.Description = unescapeTrim(extractDescription(block))

		if mp := priceRe.FindStringSubmatch(block); mp != nil {
			item.Price, _ = strconv.ParseFloat(mp[1], 64)
		}

		if mt := inlineTagsRe.FindStringSubmatch(block); mt != nil {
			for _, t := range strings.Split(mt[1], ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					item.Tags = append(item.Tags, t)
				}
			}
		}

		if md := inlineDurRe.FindStringSubmatch(block); md != nil {
			item.Duration = ParseDuration(md[1])
		}

		items = append(items, item)
	}
	return items
}

func unescapeTrim(s string) string {
	return strings.TrimSpace(html.UnescapeString(s))
}

func extractTitle(block string) string {
	if m := titleBlockRe.FindStringSubmatch(block); m != nil {
		return strings.TrimSpace(m[1])
	}
	if m := titleH4Re.FindStringSubmatch(block); m != nil {
		return strings.TrimSpace(m[1])
	}
	if m := titleH3Re.FindStringSubmatch(block); m != nil {
		title := strings.TrimSpace(m[1])
		if title != "" {
			return title
		}
	}
	return ""
}

func extractDescription(block string) string {
	if m := descStoryRe.FindStringSubmatch(block); m != nil {
		return strings.TrimSpace(m[1])
	}
	var best string
	for _, m := range descRe.FindAllStringSubmatch(block, -1) {
		text := strings.TrimSpace(m[1])
		if len(text) > len(best) && !strings.Contains(text, "addtocart") && !strings.HasPrefix(text, "Tags:") {
			best = text
		}
	}
	return best
}

func buildHrefRe(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`href="(` + regexp.QuoteMeta(prefix) + `/(\d+)/[^"#]+)`)
}

// --- detail page ---

var (
	detailTagsRe       = regexp.MustCompile(`<h4[^>]*class="customhcolor"[^>]*>([^<]+)</h4>`)
	detailPerformersRe = regexp.MustCompile(`<h3[^>]*class="customhcolor"[^>]*>([^<]*)</h3>`)
	detailDescRe       = regexp.MustCompile(`(?s)class="customhcolor2"[^>]*>(.*?)<h[34][^>]*class="customhcolor"`)
	detailDescFallRe   = regexp.MustCompile(`(?s)class="customhcolor2"[^>]*>(.*?)</(?:div|h2)>`)
	detailDurationRe   = regexp.MustCompile(`video duration <strong>(\d{2}:\d{2}:\d{2})</strong>`)
	tagRe              = regexp.MustCompile(`<[^>]*>`)
)

type DetailData struct {
	Tags        []string
	Performers  []string
	Description string
	Duration    int
}

func (s *Scraper) fetchDetail(ctx context.Context, path string) (*DetailData, error) {
	rawURL := s.base + "/" + path

	body, err := fetchPage(ctx, s.client, rawURL)
	if err != nil {
		return nil, err
	}

	return ParseDetail(body), nil
}

func ParseDetail(body []byte) *DetailData {
	d := &DetailData{}

	if m := detailTagsRe.FindSubmatch(body); m != nil {
		raw := strings.TrimSpace(string(m[1]))
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				d.Tags = append(d.Tags, t)
			}
		}
	}

	if m := detailPerformersRe.FindSubmatch(body); m != nil {
		d.Performers = splitPerformers(html.UnescapeString(string(m[1])))
	}

	m := detailDescRe.FindSubmatch(body)
	if m == nil {
		m = detailDescFallRe.FindSubmatch(body)
	}
	if m != nil {
		text := html.UnescapeString(tagRe.ReplaceAllString(string(m[1]), " "))
		d.Description = strings.Join(strings.Fields(text), " ")
	}

	if m := detailDurationRe.FindSubmatch(body); m != nil {
		d.Duration = ParseDuration(string(m[1]))
	}

	return d
}

// --- pagination ---

var pageNavRe = regexp.MustCompile(`(?s)<div class='pagenav'>(.*?)</div>`)

func buildPageLinkRe(prefix string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(prefix) + `/page/(\d+)`)
}

func maxPageFromNavWithRe(body []byte, pageLinkRe *regexp.Regexp) int {
	pm := pageNavRe.FindSubmatch(body)
	if pm == nil {
		return 0
	}

	maxPage := 0
	for _, m := range pageLinkRe.FindAllSubmatch(pm[1], -1) {
		if n, _ := strconv.Atoi(string(m[1])); n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

func maxPageFromNav(body []byte, videoPrefix string) int {
	return maxPageFromNavWithRe(body, buildPageLinkRe(videoPrefix))
}

func (s *Scraper) hasNextPage(body []byte, currentPage int) bool {
	return currentPage < maxPageFromNavWithRe(body, s.pageLinkRe)
}

func HasNextPage(body []byte, videoPrefix string, currentPage int) bool {
	mp := maxPageFromNav(body, videoPrefix)
	return currentPage < mp
}

func (s *Scraper) estimateTotal(body []byte, firstPageCount int) int {
	mp := maxPageFromNavWithRe(body, s.pageLinkRe)
	if mp == 0 {
		return firstPageCount
	}
	return mp * firstPageCount
}

func EstimateTotal(body []byte, videoPrefix string, firstPageCount int) int {
	mp := maxPageFromNav(body, videoPrefix)
	if mp == 0 {
		return firstPageCount
	}
	return mp * firstPageCount
}

// --- HTTP ---

func fetchPage(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// --- scene conversion ---

func ToScene(cfg SiteConfig, base, studioURL string, item ListItem, now time.Time) models.Scene {
	sceneURL := item.Href
	if sceneURL != "" && !strings.HasPrefix(sceneURL, "http") {
		sceneURL = base + "/" + strings.TrimPrefix(sceneURL, "/")
	}

	thumb := item.Thumbnail
	if thumb != "" && !strings.HasPrefix(thumb, "http") {
		if !strings.HasPrefix(thumb, "/") {
			thumb = "/" + thumb
		}
		thumb = base + thumb
	}

	sc := models.Scene{
		ID:          item.ID,
		SiteID:      cfg.SiteID,
		StudioURL:   studioURL,
		Title:       item.Title,
		URL:         sceneURL,
		Date:        item.Date,
		Description: item.Description,
		Thumbnail:   thumb,
		Performers:  item.Performers,
		Tags:        item.Tags,
		Duration:    item.Duration,
		Studio:      cfg.Studio,
		ScrapedAt:   now,
	}

	if item.Category != "" {
		sc.Categories = []string{item.Category}
	}

	if item.Price > 0 {
		sc.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: item.Price,
		})
	}

	return sc
}

// --- date parsing ---

var monthsMap = map[string]time.Month{
	"january": time.January, "february": time.February, "march": time.March,
	"april": time.April, "may": time.May, "june": time.June,
	"july": time.July, "august": time.August, "september": time.September,
	"october": time.October, "november": time.November, "december": time.December,
}

var dateParseRe = regexp.MustCompile(`(?i)(\w+)\s+(\d+)\w*\s+(\d{4})`)

func ParseDate(s string) time.Time {
	m := dateParseRe.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}
	}
	month, ok := monthsMap[strings.ToLower(m[1])]
	if !ok {
		return time.Time{}
	}
	day, _ := strconv.Atoi(m[2])
	year, _ := strconv.Atoi(m[3])
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func ParseDuration(s string) int {
	parts := strings.Split(s, ":")
	total := 0
	for _, p := range parts {
		n, _ := strconv.Atoi(p)
		total = total*60 + n
	}
	return total
}

func titleFromSlug(href string) string {
	parts := strings.Split(href, "/")
	if len(parts) < 2 {
		return ""
	}
	slug := parts[len(parts)-1]
	words := strings.Split(slug, "-")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
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
