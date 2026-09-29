package faphouse

import (
	"context"
	"encoding/json"
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

const (
	defaultBase = "https://faphouse.com"
	perPage     = 60
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "faphouse" }

func (s *Scraper) Patterns() []string {
	return []string{
		"faphouse.com/models/{slug}",
		"faphouse.com/studios/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?faphouse\.com/(models|studios)/([a-zA-Z0-9_-]+)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	typePath, slug := parseStudioURL(studioURL)
	if slug == "" {
		return nil, fmt.Errorf("faphouse: cannot extract slug from %q", studioURL)
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, typePath, slug, opts, out)
	return out, nil
}

func parseStudioURL(u string) (string, string) {
	if m := matchRe.FindStringSubmatch(u); m != nil {
		return m[1], m[2]
	}
	parts := strings.Split(strings.TrimRight(u, "/"), "/")
	if len(parts) >= 2 {
		t := parts[len(parts)-2]
		s := parts[len(parts)-1]
		if t == "models" || t == "studios" {
			return t, s
		}
	}
	return "", ""
}

type card struct {
	id          string
	title       string
	detailPath  string
	duration    int
	thumbnail   string
	preview     string
	price       float64
	studioName  string
	date        time.Time
	description string
	categories  []string
	performers  []string
}

func (s *Scraper) run(ctx context.Context, studioURL, typePath, slug string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	var lastTotal int
	scraper.Paginate(ctx, opts, "faphouse", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/%s/%s?sort=new&page=%d", s.base, typePath, slug, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		cards, total := parseListingPage(body)
		if len(cards) == 0 {
			return scraper.PageResult{}, nil
		}
		if total > 0 {
			lastTotal = total
		}

		for i := range cards {
			c := &cards[i]
			if c.detailPath != "" {
				if !scraper.Pace(ctx, opts.Delay) {
					return scraper.PageResult{}, ctx.Err()
				}
				detailBody, err := s.fetchPage(ctx, s.base+c.detailPath)
				if err == nil {
					d := parseDetailPage(detailBody)
					c.date = d.date
					c.description = d.description
					c.categories = d.categories
					c.performers = d.performers
				}
			}
		}

		scenes := make([]models.Scene, len(cards))
		for i, c := range cards {
			scenes[i] = toScene(c, studioURL, now)
		}

		done := (lastTotal > 0 && page*perPage >= lastTotal) || len(cards) < perPage
		return scraper.PageResult{Scenes: scenes, Total: total, Done: done}, nil
	})
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// --- listing page parsing ---

var (
	cardStartRe    = regexp.MustCompile(`data-test-id="video-thumb-(\d+)"`)
	videoHrefRe    = regexp.MustCompile(`href="(/videos/[^"]+)"`)
	durationSpanRe = regexp.MustCompile(`<span>(\d+:\d+(?::\d+)?)</span>`)
	imgTagRe       = regexp.MustCompile(`(?s)<img\s[^>]+>`)
	srcAttrRe      = regexp.MustCompile(`src="([^"]+)"`)
	altAttrRe      = regexp.MustCompile(`alt="([^"]*)"`)
	priceAttrRe    = regexp.MustCompile(`data-el-price="([^"]+)"`)
	previewAttrRe  = regexp.MustCompile(`data-el-video="([^"]+)"`)
	studioLinkRe   = regexp.MustCompile(`class="t-ti-s"[^>]*>([^<]+)`)
	totalCountRe   = regexp.MustCompile(`switcher-block__counter">(\d+)`)
)

func parseListingPage(body []byte) ([]card, int) {
	total := 0
	if m := totalCountRe.FindSubmatch(body); m != nil {
		total, _ = strconv.Atoi(string(m[1]))
	}

	locs := cardStartRe.FindAllSubmatchIndex(body, -1)
	cards := make([]card, 0, len(locs))

	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		c := card{
			id: string(body[loc[2]:loc[3]]),
		}

		if m := videoHrefRe.FindSubmatch(block); m != nil {
			c.detailPath = string(m[1])
		}
		if m := durationSpanRe.FindSubmatch(block); m != nil {
			c.duration = parseutil.ParseDurationColon(string(m[1]))
		}
		if imgTag := imgTagRe.FindSubmatch(block); imgTag != nil {
			if m := altAttrRe.FindSubmatch(imgTag[0]); m != nil {
				c.title = html.UnescapeString(strings.TrimSpace(string(m[1])))
			}
			if m := srcAttrRe.FindSubmatch(imgTag[0]); m != nil {
				c.thumbnail = string(m[1])
			}
		}
		if m := priceAttrRe.FindSubmatch(block); m != nil {
			c.price = parsePrice(string(m[1]))
		}
		if m := previewAttrRe.FindSubmatch(block); m != nil {
			c.preview = string(m[1])
		}
		if m := studioLinkRe.FindSubmatch(block); m != nil {
			c.studioName = html.UnescapeString(strings.TrimSpace(string(m[1])))
		}

		cards = append(cards, c)
	}

	return cards, total
}

// --- detail page parsing ---

type detailInfo struct {
	date        time.Time
	description string
	categories  []string
	performers  []string
}

type videoMeta struct {
	PublishedAt   string   `json:"publishedAt"`
	PornstarNames []string `json:"pornstarNames"`
}

type viewState struct {
	Video videoMeta `json:"video"`
}

var (
	viewStateRe = regexp.MustCompile(`(?s)<script id="view-state-data"[^>]*>(.*?)</script>`)
	publishRe   = regexp.MustCompile(`video-publish-date">Published:\s*(\d{2}\.\d{2}\.\d{4})`)
	descRe      = regexp.MustCompile(`(?s)video-info-details__description[^>]*>.*?<p>(.*?)</p>`)
	categoryRe  = regexp.MustCompile(`class="vid-c"[^>]*href="/c/[^"]*"[^>]*>([^<]+)`)
)

func parseDetailPage(body []byte) detailInfo {
	var info detailInfo

	if m := viewStateRe.FindSubmatch(body); m != nil {
		var vs viewState
		if json.Unmarshal(m[1], &vs) == nil {
			if vs.Video.PublishedAt != "" {
				if t, err := time.Parse("2006-01-02", vs.Video.PublishedAt); err == nil {
					info.date = t.UTC()
				}
			}
			info.performers = vs.Video.PornstarNames
		}
	}

	if info.date.IsZero() {
		if m := publishRe.FindSubmatch(body); m != nil {
			info.date = parseDDMMYYYY(string(m[1]))
		}
	}

	if m := descRe.FindSubmatch(body); m != nil {
		info.description = cleanDescription(string(m[1]))
	}

	for _, m := range categoryRe.FindAllSubmatch(body, -1) {
		cat := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if cat != "" {
			info.categories = append(info.categories, cat)
		}
	}

	return info
}

// --- helpers ---

func parsePrice(s string) float64 {
	s = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '.' {
			return r
		}
		if r == ',' {
			return '.'
		}
		return -1
	}, s)
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func parseDDMMYYYY(s string) time.Time {
	t, err := time.Parse("02.01.2006", s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

var brRe = regexp.MustCompile(`(?i)<br\s*/?>`)

func cleanDescription(s string) string {
	s = brRe.ReplaceAllString(s, "\n")
	s = html.UnescapeString(s)
	return strings.TrimSpace(s)
}

func toScene(c card, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          c.id,
		SiteID:      "faphouse",
		StudioURL:   studioURL,
		Title:       c.title,
		URL:         defaultBase + c.detailPath,
		Duration:    c.duration,
		Thumbnail:   c.thumbnail,
		Preview:     c.preview,
		Description: c.description,
		Studio:      c.studioName,
		Categories:  c.categories,
		Date:        c.date,
		ScrapedAt:   now,
	}

	if len(c.performers) > 0 {
		sc.Performers = c.performers
	} else if c.studioName != "" {
		sc.Performers = []string{c.studioName}
	}

	if c.price > 0 {
		sc.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: c.price,
		})
	}

	return sc
}
