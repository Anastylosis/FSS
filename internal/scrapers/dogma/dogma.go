// Package dogma scrapes Dogma (dogma.co.jp), a Japanese label selling each
// release through a PrestaShop storefront behind an age gate.
// See docs/scrapers.md.
package dogma

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
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

const (
	siteID     = "dogma"
	studioName = "Dogma"
	siteBase   = "http://www.dogma.co.jp"
	// categoryPath is the DVD catalogue; download and original are the same
	// releases under other delivery terms.
	categoryPath   = "/12-dvd"
	defaultWorkers = 4
	maxPages       = 100
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?dogma\.co\.jp(?:/|$)`)
	productRe  = regexp.MustCompile(`/home/(\d+)-([a-z0-9-]+)\.html`)
	titleRe    = regexp.MustCompile(`(?s)<h1[^>]*itemprop="name"[^>]*>(.*?)</h1>`)
	skuRe      = regexp.MustCompile(`(?s)itemprop="sku"[^>]*>(.*?)</span>`)
	featureRe  = regexp.MustCompile(`(?s)<li>([^<:]{2,12})\s*:\s*(.*?)</li>`)
	linkTextRe = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	descRe     = regexp.MustCompile(`(?s)<div class="rte">(.*?)</div>`)
	ogImageRe  = regexp.MustCompile(`<meta property="og:image" content="([^"]*)"`)
	priceRe    = regexp.MustCompile(`販売価格\s*:\s*<strong[^>]*>\s*¥\s*([\d,]+)`)
	minutesRe  = regexp.MustCompile(`(\d+)\s*分`)
	dateRe     = regexp.MustCompile(`(\d{4})/(\d{1,2})/(\d{1,2})`)
	scriptRe   = regexp.MustCompile(`(?s)<script.*?</script>`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	client *http.Client
	base   string
	// gate guards the one-time age-gate POST, which the whole run shares.
	gate sync.Once
}

func New() *Scraper {
	jar, _ := cookiejar.New(nil)
	c := httpx.NewClient(30 * time.Second)
	c.Jar = jar
	return &Scraper{client: c, base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"dogma.co.jp", "dogma.co.jp/12-dvd"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	s.gate.Do(func() { s.enter(ctx) })

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if page > maxPages {
			return scraper.PageResult{Done: true}, nil
		}
		refs, err := s.fetchListing(ctx, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		fresh := refs[:0]
		for _, r := range refs {
			if seen[r.id] {
				continue
			}
			seen[r.id] = true
			fresh = append(fresh, r)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{}, nil
		}
		return scraper.PageResult{Scenes: s.fetchProducts(ctx, fresh, studioURL, workers, opts.Delay, now, out)}, nil
	})
}

// enter posts the age gate's own form, which is a button rather than a login —
// every page is the gate until the session cookie it sets is held.
func (s *Scraper) enter(ctx context.Context) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     s.base + "/",
		Method:  http.MethodPost,
		Body:    []byte("chk=1"),
		Headers: gateHeaders(),
	})
	if err != nil {
		scraper.Debugf(1, "dogma: age gate: %v", err)
		return
	}
	_ = resp.Body.Close()
	scraper.Debugf(1, "dogma: age gate passed")
}

func gateHeaders() map[string]string {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	h["Content-Type"] = "application/x-www-form-urlencoded"
	return h
}

type productRef struct{ id, slug string }

func (s *Scraper) fetchListing(ctx context.Context, page int) ([]productRef, error) {
	u := fmt.Sprintf("%s%s", s.base, categoryPath)
	if page > 1 {
		u = fmt.Sprintf("%s%s?p=%d", s.base, categoryPath, page)
	}
	body, err := s.fetchPage(ctx, u)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var refs []productRef
	for _, m := range productRe.FindAllStringSubmatch(body, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		refs = append(refs, productRef{id: m[1], slug: m[2]})
	}
	return refs, nil
}

func (s *Scraper) fetchProducts(ctx context.Context, refs []productRef, studioURL string, workers int, pause time.Duration, now time.Time, out chan<- scraper.SceneResult) []models.Scene {
	results := make([]*models.Scene, len(refs))
	work := make(chan int)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range work {
				if pause > 0 {
					select {
					case <-time.After(pause):
					case <-ctx.Done():
						return
					}
				}
				pageURL := fmt.Sprintf("%s/home/%s-%s.html", s.base, refs[idx].id, refs[idx].slug)
				body, err := s.fetchPage(ctx, pageURL)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				scene, err := parseProduct(body, refs[idx], studioURL, now)
				if err != nil {
					select {
					case out <- scraper.Error(scraper.ParseError(pageURL, err)):
					case <-ctx.Done():
						return
					}
					continue
				}
				results[idx] = &scene
			}
		}()
	}
	for i := range refs {
		select {
		case work <- i:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return nil
		}
	}
	close(work)
	wg.Wait()

	scenes := make([]models.Scene, 0, len(refs))
	for _, sc := range results {
		if sc != nil {
			scenes = append(scenes, *sc)
		}
	}
	return scenes
}

// parseProduct reads the store's feature list: 女優 (cast), 監督 (director),
// シリーズ (series), ジャンル (tags), 収録時間 (runtime in minutes) and the two
// release dates, of which the DVD one is the release proper.
func parseProduct(body string, ref productRef, studioURL string, now time.Time) (models.Scene, error) {
	page := scriptRe.ReplaceAllString(body, "")

	title := ""
	if m := titleRe.FindStringSubmatch(page); m != nil {
		title = cleanText(m[1])
	}
	if title == "" {
		return models.Scene{}, fmt.Errorf("no product title on the page")
	}

	scene := models.Scene{
		ID:        ref.slug,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     title,
		URL:       fmt.Sprintf("%s/home/%s-%s.html", siteBase, ref.id, ref.slug),
		ScrapedAt: now,
	}
	if m := skuRe.FindStringSubmatch(page); m != nil {
		if sku := strings.SplitN(cleanText(m[1]), "/", 2)[0]; sku != "" {
			scene.ID = sku
		}
	}
	if m := descRe.FindStringSubmatch(page); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := ogImageRe.FindStringSubmatch(body); m != nil {
		scene.Thumbnail = html.UnescapeString(m[1])
	}

	var streamDate time.Time
	for _, m := range featureRe.FindAllStringSubmatch(page, -1) {
		label, value := strings.TrimSpace(m[1]), m[2]
		switch label {
		case "女優":
			scene.Performers = linkTexts(value)
		case "監督":
			if d := linkTexts(value); len(d) > 0 {
				scene.Director = d[0]
			}
		case "シリーズ":
			if series := linkTexts(value); len(series) > 0 {
				scene.Series = series[0]
			}
		case "ジャンル":
			scene.Tags = linkTexts(value)
		case "収録時間":
			if mins := minutesRe.FindStringSubmatch(cleanText(value)); mins != nil {
				if n, err := strconv.Atoi(mins[1]); err == nil {
					scene.Duration = n * 60
				}
			}
		case "DVD発売日":
			if d := parseDate(value); !d.IsZero() {
				scene.Date = d
			}
		case "配信開始日":
			streamDate = parseDate(value)
		}
	}
	// A download-only release has no DVD date.
	if scene.Date.IsZero() {
		scene.Date = streamDate
	}

	if m := priceRe.FindStringSubmatch(page); m != nil {
		if price, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64); err == nil && price > 0 {
			scene.AddPrice(models.PriceSnapshot{Date: now, Regular: price})
		}
	}
	return scene, nil
}

func parseDate(value string) time.Time {
	m := dateRe.FindStringSubmatch(cleanText(value))
	if m == nil {
		return time.Time{}
	}
	t, err := parseutil.TryParseDate(fmt.Sprintf("%s/%s/%s", m[1], m[2], m[3]), "2006/1/2")
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func linkTexts(cell string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range linkTextRe.FindAllStringSubmatch(cell, -1) {
		v := cleanText(m[1])
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
