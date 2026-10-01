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
	// totalRe reads "2442 件中 1 件目から 60 件を表示しています".
	totalRe = regexp.MustCompile(`(\d+)\s*件中`)
	// gateRe recognises the age gate's own form field.
	gateRe = regexp.MustCompile(`name="chk"`)
)

type Scraper struct {
	client *http.Client
	base   string
	// gateMu guards the age-gate POST, which every run in the process shares
	// once it has succeeded.
	gateMu     sync.Mutex
	gatePassed bool
}

// clientTimeout clears the origin's own latency with room to spare: a product
// page takes 4–10s and a 60-product listing ~14s, live-measured in 2026-10.
const clientTimeout = 60 * time.Second

func New() *Scraper {
	jar, _ := cookiejar.New(nil)
	c := httpx.NewClient(clientTimeout)
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

// run walks the listing serially and streams each product as its detail
// page arrives. It does not use scraper.Paginate, which emits a page only
// once every detail on it is in: at 60 products a page and 4–10s a product
// page, that held back the first scene for two minutes and lost the whole
// page to any cancellation.
func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	send := func(r scraper.SceneResult) bool {
		select {
		case out <- r:
			return true
		case <-ctx.Done():
			return false
		}
	}

	if err := s.enter(ctx); err != nil {
		send(scraper.Error(fmt.Errorf("age gate: %w", err)))
		return
	}

	workers := scraper.WorkerCount(opts, defaultWorkers)
	now := time.Now().UTC()
	seen := map[string]bool{}
	total := 0

	for page := 1; page <= maxPages; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "dogma: fetching page %d", page)
		listing, err := s.fetchListing(ctx, page)
		if err != nil {
			send(scraper.Error(fmt.Errorf("page %d: %w", page, err)))
			return
		}
		if page == 1 {
			if len(listing.refs) == 0 {
				if listing.isGate {
					// The session lapsed; let the next run post the gate again.
					s.gateMu.Lock()
					s.gatePassed = false
					s.gateMu.Unlock()
				}
				send(scraper.Error(scraper.ParseError(listing.url, listing.emptyReason())))
				return
			}
			total = listing.total
			if total > 0 {
				scraper.Debugf(1, "dogma: %d total scenes", total)
				if !send(scraper.Progress(total)) {
					return
				}
			}
		}

		// A known product ends the walk at the end of this page, as
		// scraper.Paginate does. The slug is the SKU on every product seen,
		// so it is checked before paying for the detail page; a product whose
		// SKU differs is still caught by its parsed ID below.
		hitKnown := false
		var fresh []productRef
		for _, r := range listing.refs {
			if seen[r.id] {
				continue
			}
			seen[r.id] = true
			if opts.KnownIDs[r.slug] {
				hitKnown = true
				continue
			}
			fresh = append(fresh, r)
		}
		if len(fresh) == 0 && !hitKnown {
			return
		}
		if s.fetchProducts(ctx, fresh, studioURL, workers, opts, now, out) {
			hitKnown = true
		}
		if ctx.Err() != nil {
			return
		}
		if hitKnown {
			scraper.Debugf(1, "dogma: page %d reached stored scenes, stopping", page)
			send(scraper.StoppedEarly())
			return
		}
		// Past the last page the store redirects to the first, so stop
		// once the advertised count has been listed.
		if total > 0 && len(seen) >= total {
			return
		}
	}
}

// enter posts the age gate's own form, which is a button rather than a login —
// every page is the gate until the session cookie it sets is held.
func (s *Scraper) enter(ctx context.Context) error {
	s.gateMu.Lock()
	defer s.gateMu.Unlock()
	if s.gatePassed {
		return nil
	}
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     s.base + "/",
		Method:  http.MethodPost,
		Body:    []byte("chk=1"),
		Headers: gateHeaders(),
	})
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	s.gatePassed = true
	scraper.Debugf(1, "dogma: age gate passed")
	return nil
}

func gateHeaders() map[string]string {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	h["Content-Type"] = "application/x-www-form-urlencoded"
	return h
}

type productRef struct{ id, slug string }

type listingPage struct {
	url    string
	refs   []productRef
	total  int
	isGate bool
}

func (l listingPage) emptyReason() error {
	if l.isGate {
		return fmt.Errorf("the listing is still the age gate after posting it")
	}
	return fmt.Errorf("no products on the first listing page")
}

func (s *Scraper) fetchListing(ctx context.Context, page int) (listingPage, error) {
	u := fmt.Sprintf("%s%s", s.base, categoryPath)
	if page > 1 {
		u = fmt.Sprintf("%s%s?p=%d", s.base, categoryPath, page)
	}
	body, err := s.fetchPage(ctx, u)
	if err != nil {
		return listingPage{}, err
	}
	return parseListing(u, body), nil
}

func parseListing(u, body string) listingPage {
	l := listingPage{url: u, isGate: gateRe.MatchString(body)}
	if m := totalRe.FindStringSubmatch(body); m != nil {
		l.total, _ = strconv.Atoi(m[1])
	}
	seen := map[string]bool{}
	for _, m := range productRe.FindAllStringSubmatch(body, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		l.refs = append(l.refs, productRef{id: m[1], slug: m[2]})
	}
	return l
}

// fetchProducts fetches and emits refs' detail pages through a worker pool,
// skipping any whose parsed ID is already stored. It reports whether it
// skipped one.
func (s *Scraper) fetchProducts(ctx context.Context, refs []productRef, studioURL string, workers int, opts scraper.ListOpts, now time.Time, out chan<- scraper.SceneResult) bool {
	if len(refs) == 0 {
		return false
	}
	if workers > len(refs) {
		workers = len(refs)
	}
	scraper.Debugf(1, "dogma: fetching %d details with %d workers", len(refs), workers)
	work := make(chan productRef)
	var wg sync.WaitGroup
	var mu sync.Mutex
	hitKnown := false

	send := func(r scraper.SceneResult) bool {
		select {
		case out <- r:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				pageURL := fmt.Sprintf("%s/home/%s-%s.html", s.base, ref.id, ref.slug)
				body, err := s.fetchPage(ctx, pageURL)
				if err != nil {
					if ctx.Err() != nil || !send(scraper.Error(err)) {
						return
					}
					continue
				}
				scene, err := parseProduct(body, ref, studioURL, now)
				if err != nil {
					if !send(scraper.Error(scraper.ParseError(pageURL, err))) {
						return
					}
					continue
				}
				if opts.KnownIDs[scene.ID] {
					mu.Lock()
					hitKnown = true
					mu.Unlock()
					continue
				}
				if !send(scraper.Scene(scene)) {
					return
				}
			}
		}()
	}
feed:
	for _, r := range refs {
		select {
		case work <- r:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	return hitKnown
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
