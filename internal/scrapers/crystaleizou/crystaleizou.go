package crystaleizou

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

const (
	siteID      = "crystaleizou"
	defaultBase = "https://www.crystal-eizou.jp/info"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?crystal-eizou\.jp(?:/|$)`)

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

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{"crystal-eizou.jp/"}
}
func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var archiveRe = regexp.MustCompile(`archive/(\d{4}_\d{2})\.html`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	body, err := s.fetchPage(ctx, s.base+"/")
	if err != nil {
		sendResult(ctx, out, scraper.Error(fmt.Errorf("fetch main page: %w", err)))
		return
	}

	months := parseArchiveMonths(body)
	if len(months) == 0 {
		sendResult(ctx, out, scraper.Error(fmt.Errorf("no archive months found")))
		return
	}

	scraper.Debugf(1, "%s: found %d archive months", siteID, len(months))

	now := time.Now().UTC()

	// Current month is on the main page; archived months are separate pages.
	// Process current (main page) first, then archives newest-to-oldest.
	pages := []string{s.base + "/"}
	for i := len(months) - 1; i >= 0; i-- {
		pages = append(pages, fmt.Sprintf("%s/archive/%s.html", s.base, months[i]))
	}

	for i, pageURL := range pages {
		if ctx.Err() != nil {
			return
		}

		if i > 0 && !scraper.Pace(ctx, opts.Delay) {
			return
		}

		var pageBody []byte
		if i == 0 {
			pageBody = body
		} else {
			scraper.Debugf(1, "%s: fetching %s", siteID, pageURL)
			pageBody, err = s.fetchPage(ctx, pageURL)
			if err != nil {
				sendResult(ctx, out, scraper.Error(err))
				return
			}
		}

		items := parseProducts(pageBody)
		for _, item := range items {
			if opts.KnownIDs[item.code] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, item.code)
				sendResult(ctx, out, scraper.StoppedEarly())
				return
			}
			if !sendResult(ctx, out, scraper.Scene(item.toScene(studioURL, s.base, pageURL, now))) {
				return
			}
		}
	}
}

var (
	itemBlockRe = regexp.MustCompile(`(?s)<div class="itemSection clearfix">(.*?)</div>\s*</div>`)
	titleRe     = regexp.MustCompile(`(?s)<p class="strong fsize14 mar10">(.*?)</p>`)
	greenRe     = regexp.MustCompile(`(?s)<p class="green mar10">(.*?)</p>`)
	thumbRe     = regexp.MustCompile(`<img src="(img/dvd/[^"]+)"`)
	perfRe      = regexp.MustCompile(`【出演女優】(.*?)</p>`)
	descRe      = regexp.MustCompile(`(?s)<p class="mar10">(.*?)</p>`)

	codeMetaRe    = regexp.MustCompile(`品番[：:]([A-Z]+-\d+)`)
	labelMetaRe   = regexp.MustCompile(`レーベル[：:]([^\s　]+)`)
	dateMetaRe    = regexp.MustCompile(`発売日[：:](\d{4}/\d{1,2}/\d{1,2})`)
	durationMetaR = regexp.MustCompile(`時間[：:](\d+)分`)
	priceMetaRe   = regexp.MustCompile(`(\d[\d,]+)円（税込）`)
)

type product struct {
	code        string
	title       string
	label       string
	date        time.Time
	duration    int
	price       float64
	performer   string
	description string
	thumbnail   string
}

func parseArchiveMonths(body []byte) []string {
	matches := archiveRe.FindAllSubmatch(body, -1)
	seen := map[string]bool{}
	var months []string
	for _, m := range matches {
		month := string(m[1])
		if !seen[month] {
			seen[month] = true
			months = append(months, month)
		}
	}
	return months
}

func parseProducts(body []byte) []product {
	blocks := itemBlockRe.FindAllSubmatch(body, -1)
	items := make([]product, 0, len(blocks))

	for _, block := range blocks {
		content := string(block[1])
		p := parseProduct(content)
		if p.code != "" {
			items = append(items, p)
		}
	}
	return items
}

func parseProduct(block string) product {
	var p product

	if m := titleRe.FindStringSubmatch(block); m != nil {
		p.title = cleanText(m[1])
	}

	if m := greenRe.FindStringSubmatch(block); m != nil {
		meta := cleanText(m[1])
		if cm := codeMetaRe.FindStringSubmatch(meta); cm != nil {
			p.code = cm[1]
		}
		if lm := labelMetaRe.FindStringSubmatch(meta); lm != nil {
			p.label = lm[1]
		}
		if dm := dateMetaRe.FindStringSubmatch(meta); dm != nil {
			if t, err := time.Parse("2006/1/2", dm[1]); err == nil {
				p.date = t.UTC()
			}
		}
		if durm := durationMetaR.FindStringSubmatch(meta); durm != nil {
			n, _ := strconv.Atoi(durm[1])
			p.duration = n * 60
		}
		if pm := priceMetaRe.FindStringSubmatch(meta); pm != nil {
			cleaned := strings.ReplaceAll(pm[1], ",", "")
			if v, err := strconv.ParseFloat(cleaned, 64); err == nil {
				p.price = v
			}
		}
	}

	if m := perfRe.FindStringSubmatch(block); m != nil {
		p.performer = cleanPerformer(m[1])
	}

	if m := descRe.FindStringSubmatch(block); m != nil {
		p.description = cleanText(m[1])
	}

	if m := thumbRe.FindStringSubmatch(block); m != nil {
		p.thumbnail = m[1]
	}

	return p
}

func cleanText(s string) string {
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "　", " ")
	return strings.TrimSpace(s)
}

var perfCleanRe = regexp.MustCompile(`（[^）]*）`)

func cleanPerformer(s string) string {
	s = cleanText(s)
	s = perfCleanRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func (p product) toScene(studioURL, base, pageURL string, now time.Time) models.Scene {
	thumb := p.thumbnail
	if thumb != "" && !strings.HasPrefix(thumb, "http") {
		thumb = base + "/" + thumb
	}

	sc := models.Scene{
		ID:        p.code,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     p.title,
		// The site publishes no per-product page — the whole catalogue lives on
		// the front page and its monthly `archive/{YYYY_MM}.html` pages — so
		// every scene used to be stored under the same bare `/info/` URL. The
		// page it was actually found on, anchored on the product code, is the
		// closest thing to an address the site offers and at least tells two
		// scenes apart.
		URL:         pageURL + "#" + p.code,
		Thumbnail:   thumb,
		Date:        p.date,
		Duration:    p.duration,
		Description: p.description,
		Studio:      p.label,
		ScrapedAt:   now,
	}

	if p.performer != "" {
		sc.Performers = splitPerformers(p.performer)
	}

	if p.price > 0 {
		sc.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: p.price,
		})
	}

	return sc
}

func splitPerformers(s string) []string {
	var result []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '、' || r == '／'
	}) {
		if v := strings.TrimSpace(p); v != "" {
			result = append(result, v)
		}
	}
	return result
}

func sendResult(ctx context.Context, ch chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case ch <- r:
		return true
	case <-ctx.Done():
		return false
	}
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
