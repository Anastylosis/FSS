package houseofyre

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

const defaultSiteBase = "https://www.houseofyre.com"

type Scraper struct {
	client   *http.Client
	siteBase string
}

func New() *Scraper {
	return &Scraper{
		client:   httpx.NewClient(30 * time.Second),
		siteBase: defaultSiteBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "houseofyre" }

func (s *Scraper) Patterns() []string {
	return []string{
		"houseofyre.com",
		"houseofyre.com/models/{name}.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?houseofyre\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listingScene)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ls := range work {
				scene, err := s.fetchDetail(ctx, ls, opts.Delay)
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

	isModel := strings.Contains(studioURL, "/models/")

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)

		if isModel {
			s.enqueueModelPage(ctx, studioURL, opts, out, work)
		} else {
			s.enqueueListingPages(ctx, opts, out, work)
		}
	}()

	wg.Wait()
}

func (s *Scraper) enqueueListingPages(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "houseofyre: fetching page %d", page)

		var pageURL string
		if page == 1 {
			pageURL = s.siteBase + "/categories/movies.html"
		} else {
			pageURL = fmt.Sprintf("%s/categories/movies_%d.html", s.siteBase, page)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		scenes := parseListingPage(body, s.siteBase)
		if len(scenes) == 0 {
			return
		}

		if page == 1 {
			maxPage := extractMaxPage(body)
			total := len(scenes) * maxPage
			scraper.Debugf(1, "houseofyre: %d total scenes", total)
			select {
			case out <- scraper.Progress(total):
			case <-ctx.Done():
				return
			}
		}

		for _, ls := range scenes {
			if opts.KnownIDs[ls.id] {
				scraper.Debugf(1, "houseofyre: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- ls:
			case <-ctx.Done():
				return
			}
		}

		if !hasNextPage(body, page) {
			return
		}
	}
}

func (s *Scraper) enqueueModelPage(ctx context.Context, modelURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	body, err := s.fetchPage(ctx, modelURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	scenes := parseListingPage(body, s.siteBase)
	select {
	case out <- scraper.Progress(len(scenes)):
	case <-ctx.Done():
		return
	}

	for _, ls := range scenes {
		if opts.KnownIDs[ls.id] {
			scraper.Debugf(1, "houseofyre: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case work <- ls:
		case <-ctx.Done():
			return
		}
	}
}

type listingScene struct {
	id         string
	url        string
	title      string
	performers []string
	date       time.Time
	duration   int
	thumb      string
	price      float64
}

var (
	blockStartRe = regexp.MustCompile(`class="latestUpdateB"\s+data-setid="(\d+)"`)
	sceneLinkRe  = regexp.MustCompile(`href="(https?://[^"]*?/scenes/[^"]+\.html)"`)
	titleRe      = regexp.MustCompile(`<h4 class="link_bright">\s*<a[^>]+>([^<]+)</a>`)
	performerRe  = regexp.MustCompile(`class="link_bright infolink"\s+href="[^"]*models/[^"]*">([^<]+)</a>`)
	dateRe       = regexp.MustCompile(`<!-- Date -->\s*(\d{2}/\d{2}/\d{4})`)
	durationRe   = regexp.MustCompile(`<i class="fas fa-video"></i>(\d+)\s*min`)
	thumbRe      = regexp.MustCompile(`poster_2x="([^"]+)"`)
	priceRe      = regexp.MustCompile(`Buy\s*\(\$([0-9.]+)\)`)
	maxPageRe    = regexp.MustCompile(`/categories/movies_(\d+)\.html`)
	descRe       = regexp.MustCompile(`(?s)class="vidImgContent[^"]*">\s*<p>(.*?)</p>`)
	blogTagsRe   = regexp.MustCompile(`(?s)class='blogTags'>(.*?)</div>`)
	tagLinkRe    = regexp.MustCompile(`(?s)<a[^>]*>(?:<[^>]*>)*([^<]+)</a>`)
)

func parseListingPage(body []byte, siteBase string) []listingScene {
	page := string(body)
	locs := blockStartRe.FindAllStringSubmatchIndex(page, -1)
	var scenes []listingScene

	for i, loc := range locs {
		id := page[loc[2]:loc[3]]
		start := loc[0]
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[start:end]

		ls := listingScene{id: id}

		if m := sceneLinkRe.FindStringSubmatch(block); m != nil {
			ls.url = m[1]
		}

		if m := titleRe.FindStringSubmatch(block); m != nil {
			ls.title = strings.TrimSpace(html.UnescapeString(m[1]))
		}

		for _, m := range performerRe.FindAllStringSubmatch(block, -1) {
			name := strings.TrimSpace(html.UnescapeString(m[1]))
			if name != "" {
				ls.performers = append(ls.performers, name)
			}
		}

		if m := dateRe.FindStringSubmatch(block); m != nil {
			if t, err := time.Parse("01/02/2006", m[1]); err == nil {
				ls.date = t.UTC()
			}
		}

		if m := durationRe.FindStringSubmatch(block); m != nil {
			mins, _ := strconv.Atoi(m[1])
			ls.duration = mins * 60
		}

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			thumb := m[1]
			if strings.HasPrefix(thumb, "/") {
				thumb = siteBase + thumb
			}
			ls.thumb = thumb
		}

		if m := priceRe.FindStringSubmatch(block); m != nil {
			ls.price, _ = strconv.ParseFloat(m[1], 64)
		}

		scenes = append(scenes, ls)
	}
	return scenes
}

func extractMaxPage(body []byte) int {
	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

func hasNextPage(body []byte, current int) bool {
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > current {
			return true
		}
	}
	return false
}

type detailData struct {
	description string
	tags        []string
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := descRe.FindSubmatch(body); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}

	if m := blogTagsRe.FindSubmatch(body); m != nil {
		for _, tm := range tagLinkRe.FindAllSubmatch(m[1], -1) {
			tag := strings.TrimSpace(html.UnescapeString(string(tm[1])))
			if tag != "" {
				d.tags = append(d.tags, tag)
			}
		}
	}

	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:         ls.id,
		SiteID:     "houseofyre",
		StudioURL:  s.siteBase,
		Title:      ls.title,
		URL:        ls.url,
		Date:       ls.date,
		Duration:   ls.duration,
		Performers: ls.performers,
		Thumbnail:  ls.thumb,
		Studio:     "House of Fyre",
		ScrapedAt:  now,
	}

	if ls.price > 0 {
		scene.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: ls.price,
		})
	}

	if ls.url != "" {
		body, err := s.fetchPage(ctx, ls.url)
		if err != nil {
			return models.Scene{}, fmt.Errorf("detail %s: %w", ls.id, err)
		}
		detail := parseDetailPage(body)
		scene.Description = detail.description
		scene.Tags = detail.tags
	}

	return scene, nil
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
