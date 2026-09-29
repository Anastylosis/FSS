package smqueenroad

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID      = "smqueenroad"
	defaultBase = "https://www.smqr.com"
	studioName  = "SM Queen Road"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?smqr\.com(?:/|$)`)

type Scraper struct {
	client   *http.Client
	siteBase string
}

func New() *Scraper {
	return &Scraper{
		client:   httpx.NewClient(30 * time.Second),
		siteBase: defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string         { return siteID }
func (s *Scraper) Patterns() []string { return []string{"smqr.com"} }
func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listingItem struct {
	id   string
	name string
}

var xmlItemRe = regexp.MustCompile(`<Items\s+ID=\\"([^"\\]+)\\"\s+Name=\\"([^"\\]*)\\"`)

func parseListingXML(body []byte) []listingItem {
	matches := xmlItemRe.FindAllSubmatch(body, -1)
	items := make([]listingItem, 0, len(matches))
	for _, m := range matches {
		id := string(m[1])
		name := unescapeXMLEntities(string(m[2]))
		if id != "" {
			items = append(items, listingItem{id: strings.TrimSpace(id), name: name})
		}
	}
	return items
}

func unescapeXMLEntities(s string) string {
	s = strings.ReplaceAll(s, "&amp;amp;", "&")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	return s
}

var (
	dateRe      = regexp.MustCompile(`<dt>発売日</dt>\s*<dd>(\d{4})年(\d{2})月(\d{2})日</dd>`)
	codeRe      = regexp.MustCompile(`<dt>品番</dt>\s*<dd>([^<]+)</dd>`)
	categoryRe  = regexp.MustCompile(`(?s)<dt>カテゴリー</dt>\s*<dd>\s*<ul[^>]*>(.*?)</ul>`)
	catLinkRe   = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
	performerRe = regexp.MustCompile(`(?s)<dt>出演者</dt>\s*<dd>\s*<ul[^>]*>(.*?)</ul>`)
	castNameRe  = regexp.MustCompile(`(?s)<a[^>]*>\s*(?:<img[^>]*>)?\s*([^<\s][^<]*?)\s*</a>`)
	eyecatchRe  = regexp.MustCompile(`poster="(/userdata/Items/eyecatch-[^"]+)"`)
	previewRe   = regexp.MustCompile(`<source\s+src="(/userdata/Items/sample-[^"]+)"`)
)

type detailData struct {
	date       time.Time
	code       string
	categories []string
	performers []string
	thumbnail  string
	preview    string
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := dateRe.FindSubmatch(body); m != nil {
		ds := fmt.Sprintf("%s-%s-%s", m[1], m[2], m[3])
		if t, err := time.Parse("2006-01-02", ds); err == nil {
			d.date = t.UTC()
		}
	}

	if m := codeRe.FindSubmatch(body); m != nil {
		d.code = strings.TrimSpace(string(m[1]))
	}

	if m := categoryRe.FindSubmatch(body); m != nil {
		for _, cm := range catLinkRe.FindAllSubmatch(m[1], -1) {
			tag := strings.TrimSpace(string(cm[1]))
			if tag != "" {
				d.categories = append(d.categories, tag)
			}
		}
	}

	if m := performerRe.FindSubmatch(body); m != nil {
		for _, pm := range castNameRe.FindAllSubmatch(m[1], -1) {
			name := strings.TrimSpace(string(pm[1]))
			if name != "" {
				d.performers = append(d.performers, name)
			}
		}
	}

	if m := eyecatchRe.FindSubmatch(body); m != nil {
		d.thumbnail = string(m[1])
	}

	if m := previewRe.FindSubmatch(body); m != nil {
		d.preview = string(m[1])
	}

	return d
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: fetching item list", siteID)
	body, err := s.fetchPage(ctx, s.siteBase+"/Front/ItemList")
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("fetch listing: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	items := parseListingXML(body)
	scraper.Debugf(1, "%s: found %d items", siteID, len(items))
	if len(items) == 0 {
		return
	}

	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching details with %d workers", siteID, workers)

	work := make(chan listingItem)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, studioURL, item)
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

	for _, item := range items {
		if opts.KnownIDs[item.id] {
			scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, item.id)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			break
		}
		select {
		case work <- item:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) fetchDetail(ctx context.Context, studioURL string, item listingItem) (models.Scene, error) {
	pageURL := fmt.Sprintf("%s/Front/ItemDetail/%s", s.siteBase, item.id)

	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", item.id, err)
	}

	d := parseDetailPage(body)

	now := time.Now().UTC()

	scene := models.Scene{
		ID:         item.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      item.name,
		URL:        pageURL,
		Thumbnail:  s.absURL(d.thumbnail),
		Preview:    s.absURL(d.preview),
		Date:       d.date,
		Performers: d.performers,
		Tags:       d.categories,
		Studio:     studioName,
		ScrapedAt:  now,
	}

	return scene, nil
}

func (s *Scraper) absURL(path string) string {
	if path == "" {
		return ""
	}
	return s.siteBase + path
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
