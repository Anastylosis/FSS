package erosarts

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	ID       string
	SiteBase string
	SiteName string
	Patterns []string
	MatchRe  *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	c := httpx.NewClient(30 * time.Second)
	c.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Scraper{cfg: cfg, client: c}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string         { return s.cfg.ID }
func (s *Scraper) Patterns() []string { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool {
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	baseURL := buildBaseURL(s.cfg.SiteBase, studioURL)
	scraper.Debugf(1, "%s: listing base %s", s.cfg.ID, baseURL)

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, baseURL, page)
		if err != nil {
			return scraper.PageResult{}, err
		}

		cards, total := parseListingPage(body, s.cfg.SiteBase)

		scenes := make([]models.Scene, len(cards))
		for i, c := range cards {
			scenes[i] = c.toScene(s.cfg, studioURL, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func buildBaseURL(siteBase, studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil || u.RawQuery == "" {
		return siteBase + "/tour.php"
	}
	q := u.Query()
	q.Del("p")
	base := siteBase + "/tour.php"
	if encoded := q.Encode(); encoded != "" {
		base += "?" + encoded
	}
	return base
}

func (s *Scraper) fetchPage(ctx context.Context, baseURL string, page int) ([]byte, error) {
	sep := "?"
	if strings.Contains(baseURL, "?") {
		sep = "&"
	}
	u := baseURL + sep + "p=" + strconv.Itoa(page)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: u,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
			"Cookie":     "free_adult=dark_mode",
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

type listingCard struct {
	id          string
	url         string
	title       string
	description string
	thumbnail   string
	date        time.Time
	performers  []string
	tags        []string
	duration    int
}

func (c listingCard) toScene(cfg SiteConfig, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:          c.id,
		SiteID:      cfg.ID,
		StudioURL:   studioURL,
		Title:       c.title,
		Description: c.description,
		URL:         c.url,
		Thumbnail:   c.thumbnail,
		Date:        c.date,
		Duration:    c.duration,
		Performers:  c.performers,
		Tags:        c.tags,
		Studio:      cfg.SiteName,
		ScrapedAt:   now,
	}
}

var (
	cardBlockRe = regexp.MustCompile(`(?s)<div class="curvy">\s*<span id="ctl">.*?</table>\s*</div>`)
	videoURLRe  = regexp.MustCompile(`<a href="/videos/(\d+)/">`)
	thumbRe     = regexp.MustCompile(`<img src="(/cover_images/[^"]+)" class="photo"`)
	titleRe     = regexp.MustCompile(`<div class="title">([^<]+)</div>`)
	descRe      = regexp.MustCompile(`(?s)<p>(.*?)(?:&nbsp;)?<a href="/videos/\d+/">more</a></p>`)
	dateRe      = regexp.MustCompile(`Date Added:\s*(\d{2}/\d{2}/\d{4})`)
	starringRe  = regexp.MustCompile(`(?s)Starring:\s*(.*?)<br>`)
	perfLinkRe  = regexp.MustCompile(`>([^<]+)</a>`)
	durationRe  = regexp.MustCompile(`Running Time:\s*(\d+)\s+mins`)
	keywordsRe  = regexp.MustCompile(`(?s)<div class="keywords">\s*(.*?)</div>`)
	kwLinkRe    = regexp.MustCompile(`>([^<]+)</a>`)
	totalRe     = regexp.MustCompile(`(\d+)\s+Video\(s\) Found`)
)

func parseListingPage(body []byte, base string) ([]listingCard, int) {
	total := 0
	if m := totalRe.FindSubmatch(body); m != nil {
		total, _ = strconv.Atoi(string(m[1]))
	}

	blocks := cardBlockRe.FindAll(body, -1)
	cards := make([]listingCard, 0, len(blocks))

	for _, block := range blocks {
		c := listingCard{}

		if m := videoURLRe.FindSubmatch(block); m != nil {
			c.id = string(m[1])
			c.url = base + "/videos/" + c.id + "/"
		}
		if c.id == "" {
			continue
		}

		if m := thumbRe.FindSubmatch(block); m != nil {
			c.thumbnail = base + string(m[1])
		}

		if m := titleRe.FindSubmatch(block); m != nil {
			t := string(m[1])
			if !strings.Contains(t, "Video(s) Found") {
				c.title = html.UnescapeString(strings.TrimSpace(t))
			}
		}
		if c.title == "" {
			for _, tm := range titleRe.FindAllSubmatch(block, -1) {
				t := string(tm[1])
				if !strings.Contains(t, "Video(s) Found") {
					c.title = html.UnescapeString(strings.TrimSpace(t))
					break
				}
			}
		}

		if m := descRe.FindSubmatch(block); m != nil {
			c.description = html.UnescapeString(strings.TrimSpace(string(m[1])))
		}

		if m := dateRe.FindSubmatch(block); m != nil {
			if t, err := time.Parse("01/02/2006", string(m[1])); err == nil {
				c.date = t.UTC()
			}
		}

		if m := starringRe.FindSubmatch(block); m != nil {
			for _, pm := range perfLinkRe.FindAllSubmatch(m[1], -1) {
				name := strings.TrimSpace(html.UnescapeString(string(pm[1])))
				if name != "" {
					c.performers = append(c.performers, name)
				}
			}
		}

		if m := durationRe.FindSubmatch(block); m != nil {
			mins, _ := strconv.Atoi(string(m[1]))
			c.duration = mins * 60
		}

		if m := keywordsRe.FindSubmatch(block); m != nil {
			for _, km := range kwLinkRe.FindAllSubmatch(m[1], -1) {
				tag := strings.TrimSpace(html.UnescapeString(string(km[1])))
				if tag != "" {
					c.tags = append(c.tags, tag)
				}
			}
		}

		cards = append(cards, c)
	}

	return cards, total
}
