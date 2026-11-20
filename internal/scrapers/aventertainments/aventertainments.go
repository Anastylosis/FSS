// Package aventertainments scrapes the DVD studio pages of AV Entertainment
// (aventertainments.com), a retailer of Japanese and Western DVDs. One
// scraper serves every studio the store lists. See docs/scrapers.md.
package aventertainments

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID         = "aventertainments"
	defaultWorkers = 4
)

var siteBase = "https://www.aventertainments.com"

var (
	hostRe      = regexp.MustCompile(`^https?://(?:www\.)?aventertainments\.com/`)
	studioIDRe  = regexp.MustCompile(`(?i)[?&]studio(?:id)?=(\d+)`)
	legacyRe    = regexp.MustCompile(`^https?://(?:www\.)?aventertainments\.com/(\d+)/(\d+)/\d+/studio_products`)
	deptRe      = regexp.MustCompile(`(?i)[?&](?:cat|dept_id)=(\d+)`)
	cardRe      = regexp.MustCompile(`(?s)<div class="single-slider-product grid-view-product">(.*?)<!--=+\s*End of grid view product`)
	proRe       = regexp.MustCompile(`detail\?pro=(\d+)`)
	cardImgRe   = regexp.MustCompile(`<img src="([^"]+)" width="190"`)
	cardTitleRe = regexp.MustCompile(`(?s)class="product-title"><a [^>]*>(.*?)</a>`)
	priceRe     = regexp.MustCompile(`class='discounted-price'>\$([\d.,]+)`)
	infoRe      = regexp.MustCompile(`(?s)<span class="title">([^<]+)</span>\s*<span class="value[^"]*">(.*?)</span>\s*</div>`)
	catBlockRe  = regexp.MustCompile(`(?s)<span class="value-category">(.*?)</span>`)
	anchorRe    = regexp.MustCompile(`(?s)<a [^>]*>(.*?)</a>`)
	dateRe      = regexp.MustCompile(`(\d{1,2})/(\d{1,2})/(\d{4})`)
	minRe       = regexp.MustCompile(`(\d+)\s*Min`)
	ldRe        = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	stripRe     = regexp.MustCompile(`<[^>]+>`)
	skipTags    = map[string]bool{"New Release": true, "Pre Release": true, "Editor's Pick": true, "Sample Movie": true, "Uncensored": true}
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"aventertainments.com/dvd/studio?studio={id}",
		"aventertainments.com/dvd/studio?lang={lang}&cat={cat}&studio={id}",
		"aventertainments.com/{cat}/{id}/{lang}/studio_products",
	}
}

// MatchesURL accepts the DVD studio page and its two legacy spellings. The
// PPV catalogue (/ppv/studio) numbers its studios separately and is not served.
func (s *Scraper) MatchesURL(u string) bool {
	if !hostRe.MatchString(u) {
		return false
	}
	_, _, ok := parseStudioURL(u)
	return ok
}

func parseStudioURL(u string) (studio, cat string, ok bool) {
	if strings.Contains(u, "/ppv/") {
		return "", "", false
	}
	cat = "29"
	if m := deptRe.FindStringSubmatch(u); m != nil {
		cat = m[1]
	}
	if m := legacyRe.FindStringSubmatch(u); m != nil {
		return m[2], m[1], true
	}
	if strings.Contains(u, "/dvd/studio") || strings.Contains(strings.ToLower(u), "studio_products.aspx") {
		if m := studioIDRe.FindStringSubmatch(u); m != nil {
			return m[1], cat, true
		}
	}
	return "", "", false
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	studio, cat, ok := parseStudioURL(studioURL)
	if !ok {
		return nil, fmt.Errorf("aventertainments: %q is not a DVD studio URL", studioURL)
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, studio, cat, opts, out)
	return out, nil
}

type card struct {
	id, title, thumb string
	price            float64
}

func (s *Scraper) run(ctx context.Context, studioURL, studio, cat string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "%s: studio %s (cat %s), %d detail workers", siteID, studio, cat, workers)

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/dvd/studio?lang=1&cat=%s&studio=%s&page=%d", siteBase, cat, studio, page)
		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseCards(body)
		scenes := make([]models.Scene, len(cards))
		errs := make([]error, len(cards))

		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		for i, c := range cards {
			scenes[i] = models.Scene{ID: c.id}
			if opts.KnownIDs[c.id] {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				if !scraper.Pace(ctx, opts.Delay) {
					errs[i] = ctx.Err()
					return
				}
				du := fmt.Sprintf("%s/dvd/detail?pro=%s&lang=1&cat=%s", siteBase, c.id, cat)
				page, err := s.get(ctx, du)
				if err != nil {
					errs[i] = err
					return
				}
				scenes[i] = toScene(c, string(page), du, studioURL, now)
			}()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return scraper.PageResult{}, err
			}
		}
		return scraper.PageResult{Scenes: scenes, Done: len(cards) == 0}, nil
	})
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func parseCards(body []byte) []card {
	var cards []card
	for _, m := range cardRe.FindAllStringSubmatch(string(body), -1) {
		pro := proRe.FindStringSubmatch(m[1])
		if pro == nil {
			continue
		}
		c := card{id: pro[1]}
		if t := cardTitleRe.FindStringSubmatch(m[1]); t != nil {
			c.title = clean(t[1])
		}
		if i := cardImgRe.FindStringSubmatch(m[1]); i != nil {
			c.thumb = i[1]
		}
		if p := priceRe.FindStringSubmatch(m[1]); p != nil {
			c.price, _ = strconv.ParseFloat(strings.ReplaceAll(p[1], ",", ""), 64)
		}
		cards = append(cards, c)
	}
	return cards
}

func toScene(c card, page, detailURL, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:        c.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     c.title,
		URL:       detailURL,
		Thumbnail: c.thumb,
		ScrapedAt: now,
	}
	if c.price > 0 {
		sc.AddPrice(models.PriceSnapshot{Date: now, Regular: c.price})
	}
	for _, m := range infoRe.FindAllStringSubmatch(page, -1) {
		label, value := strings.TrimSpace(m[1]), m[2]
		switch label {
		case "Starring":
			for _, a := range anchorRe.FindAllStringSubmatch(value, -1) {
				if name := clean(a[1]); name != "" {
					sc.Performers = append(sc.Performers, name)
				}
			}
		case "Studio":
			sc.Studio = clean(value)
		case "Series":
			sc.Series = clean(value)
		case "Date":
			if d := dateRe.FindStringSubmatch(value); d != nil {
				mo, _ := strconv.Atoi(d[1])
				day, _ := strconv.Atoi(d[2])
				y, _ := strconv.Atoi(d[3])
				sc.Date = time.Date(y, time.Month(mo), day, 0, 0, 0, 0, time.UTC)
			}
		case "Play Time":
			if d := minRe.FindStringSubmatch(value); d != nil {
				n, _ := strconv.Atoi(d[1])
				sc.Duration = n * 60
			}
		}
	}
	if m := catBlockRe.FindStringSubmatch(page); m != nil {
		for _, a := range anchorRe.FindAllStringSubmatch(m[1], -1) {
			if tag := clean(a[1]); tag != "" && !skipTags[tag] {
				sc.Tags = append(sc.Tags, tag)
			}
		}
	}
	if sc.Date.IsZero() {
		if m := ldRe.FindStringSubmatch(page); m != nil {
			var ld struct {
				UploadDate string `json:"uploadDate"`
			}
			if json.Unmarshal([]byte(m[1]), &ld) == nil {
				if t, err := time.Parse(time.RFC3339, ld.UploadDate); err == nil {
					sc.Date = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
				}
			}
		}
	}
	return sc
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, " "))), " ")
}
