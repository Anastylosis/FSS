package ftvutil

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

type SiteConfig struct {
	SiteID    string
	Domain    string
	Studio    string
	TitleSite string // e.g. "FTVGirls.com" or "FTVMilfs.com" in <title>
}

type Scraper struct {
	cfg     SiteConfig
	Client  *http.Client
	Base    string
	matchRe *regexp.Regexp
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		Base:    "https://" + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }
func (s *Scraper) MatchesURL(u string) bool {
	return s.matchRe.MatchString(u)
}
func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/updates.html",
	}
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.Run(ctx, studioURL, opts, out)
	return out, nil
}

type ListingEntry struct {
	ID       string
	Name     string
	Date     time.Time
	Duration int
	Tags     []string
	Thumb    string
	Desc     string
}

var (
	containerRe = regexp.MustCompile(`(?s)<div class="ModelContainer">(.*?)</div><!-- ModelContainer -->`)
	listNameRe  = regexp.MustCompile(`class="ModelName"><h2>(.*?)</h2>`)
	listDateRe  = regexp.MustCompile(`class="UpdateDate"><h3>(.*?)</h3>`)
	listDurRe   = regexp.MustCompile(`(?s)class="VideoTime">.*?<h3>(\d+)\s*mins?</h3>`)
	listTagRe   = regexp.MustCompile(`updatesCategories/\w+\.png"\s*title="([^"]*)"`)
	listBioRe   = regexp.MustCompile(`(?s)class="Bio">\s*<p>(.*?)</p>`)
	// Two ID extraction strategies: from thumbnail filename or from link href.
	thumbIDRe = regexp.MustCompile(`ModelPhotoWide"\s*src="([^"]*tour-(\d+)\.jpg)"`)
	hrefIDRe  = regexp.MustCompile(`<a\s+href="/update/[^"]*?-(\d+)\.html"><img\s+class="ModelPhotoWide"\s*src="([^"]*)"`)
	thumbRe   = regexp.MustCompile(`ModelPhotoWide"\s*src="([^"]*)"`)
)

func ParseDate(raw string) time.Time {
	raw = strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if t, err := time.Parse("Jan 2, 2006", raw); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func ParseListingPage(body []byte) []ListingEntry {
	matches := containerRe.FindAllSubmatch(body, -1)
	entries := make([]ListingEntry, 0, len(matches))

	for _, m := range matches {
		block := string(m[1])
		var e ListingEntry

		if nm := listNameRe.FindStringSubmatch(block); nm != nil {
			e.Name = strings.TrimSpace(html.UnescapeString(nm[1]))
		}
		if dm := listDateRe.FindStringSubmatch(block); dm != nil {
			e.Date = ParseDate(dm[1])
		}
		if dur := listDurRe.FindStringSubmatch(block); dur != nil {
			mins, _ := strconv.Atoi(dur[1])
			e.Duration = mins * 60
		}
		for _, tm := range listTagRe.FindAllStringSubmatch(block, -1) {
			parts := strings.SplitN(tm[1], " - ", 2)
			tag := strings.TrimSpace(parts[0])
			if tag != "" {
				e.Tags = append(e.Tags, tag)
			}
		}
		if bio := listBioRe.FindStringSubmatch(block); bio != nil {
			e.Desc = strings.TrimSpace(html.UnescapeString(bio[1]))
		}

		// Try href-based ID extraction first (ftvgirls), then thumbnail-based (ftvmilfs).
		if hm := hrefIDRe.FindStringSubmatch(block); hm != nil {
			e.ID = hm[1]
			e.Thumb = hm[2]
		} else if th := thumbIDRe.FindStringSubmatch(block); th != nil {
			e.Thumb = th[1]
			e.ID = th[2]
		} else if th := thumbRe.FindStringSubmatch(block); th != nil {
			e.Thumb = th[1]
		}

		if e.ID == "" || e.Name == "" {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

type DetailData struct {
	Name   string
	Age    int
	Height string
	Figure string
	Date   time.Time
	Desc   string
	Thumb  string
}

var (
	dataTitleRe = regexp.MustCompile(`data-title="[^"]*<span>([^<]*)</span>[^"]*Age:[^"]*<span>(\d+)</span>[^"]*Figure:[^"]*<span>([^<]*)</span>[^"]*Release date:[^"]*<span>([^<]*)</span>"`)
	detTitleRe  = regexp.MustCompile(`<title>(.*?) on FTV\w+\.com Released (.*?)!`)
	detBioRe    = regexp.MustCompile(`(?s)id="Bio">\s*<p>(.*?)</p>`)
	detThumbRe  = regexp.MustCompile(`id="Magazine"\s*src="([^"]*)"`)
	detHeightRe = regexp.MustCompile(`Height:</b>\s*([^<]+)`)
)

func ParseDetailPage(body []byte) DetailData {
	var d DetailData
	page := string(body)

	if m := dataTitleRe.FindStringSubmatch(page); m != nil {
		d.Name = strings.TrimSpace(m[1])
		d.Age, _ = strconv.Atoi(m[2])
		d.Figure = strings.TrimSpace(m[3])
		d.Date = ParseDate(m[4])
	} else if m := detTitleRe.FindStringSubmatch(page); m != nil {
		d.Name = strings.TrimSpace(m[1])
		d.Date = ParseDate(m[2])
	}

	if m := detHeightRe.FindStringSubmatch(page); m != nil {
		d.Height = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := detBioRe.FindStringSubmatch(page); m != nil {
		d.Desc = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := detThumbRe.FindStringSubmatch(page); m != nil {
		d.Thumb = m[1]
	}

	return d
}

func (s *Scraper) Run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	scraper.Debugf(1, "%s: fetching listing page", s.cfg.SiteID)
	body, err := s.FetchPage(ctx, s.Base+"/updates.html")
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("listing: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	entries := ParseListingPage(body)
	if len(entries) == 0 {
		select {
		case out <- scraper.Error(fmt.Errorf("no scenes found on listing page")):
		case <-ctx.Done():
		}
		return
	}

	enrichment := make(map[string]ListingEntry, len(entries))
	for _, e := range entries {
		enrichment[e.ID] = e
	}

	latestID, _ := strconv.Atoi(entries[0].ID)
	if latestID <= 0 {
		return
	}

	scraper.Debugf(1, "%s: %d total scenes", s.cfg.SiteID, latestID)
	select {
	case out <- scraper.Progress(latestID):
	case <-ctx.Done():
		return
	}

	work := make(chan int)
	var wg sync.WaitGroup
	scraper.Debugf(1, "%s: fetching %d detail pages with %d workers", s.cfg.SiteID, latestID, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range work {
				scene, err := s.fetchScene(ctx, id, studioURL, enrichment, opts.Delay)
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)
		for id := latestID; id >= 1; id-- {
			idStr := strconv.Itoa(id)
			if opts.KnownIDs[idStr] {
				scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- id:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
}

func (s *Scraper) fetchScene(ctx context.Context, id int, studioURL string, enrichment map[string]ListingEntry, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	idStr := strconv.Itoa(id)
	url := fmt.Sprintf("%s/update/x-%d.html", s.Base, id)

	body, err := s.FetchPage(ctx, url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %d: %w", id, err)
	}

	detail := ParseDetailPage(body)
	if detail.Name == "" {
		return models.Scene{}, fmt.Errorf("detail %d: empty page", id)
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:          idStr,
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		URL:         url,
		Title:       detail.Name,
		Date:        detail.Date,
		Description: detail.Desc,
		Thumbnail:   detail.Thumb,
		Performers:  []string{detail.Name},
		Studio:      s.cfg.Studio,
		ScrapedAt:   now,
	}

	if le, ok := enrichment[idStr]; ok {
		scene.Duration = le.Duration
		scene.Tags = le.Tags
		if scene.Thumbnail == "" {
			scene.Thumbnail = le.Thumb
		}
	}

	return scene, nil
}

func (s *Scraper) FetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}
