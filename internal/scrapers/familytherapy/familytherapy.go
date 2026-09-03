package familytherapy

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/internal/scrapers/wputil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type Scraper struct {
	client   *http.Client
	siteBase string
	headers  map[string]string
}

func New() *Scraper {
	return &Scraper{
		client:   httpx.NewClient(30 * time.Second),
		siteBase: "https://familytherapyxxx.com",
		headers:  httpx.BrowserHeaders(httpx.UserAgentFirefox),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "familytherapy" }

func (s *Scraper) Patterns() []string {
	return []string{"familytherapyxxx.com"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?familytherapyxxx\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		wputil.RunWorkerPool(ctx, s.client, s.headers,
			[]string{s.siteBase + "/sitemap.xml"},
			studioURL, opts, parsePage, out)
	}()
	return out, nil
}

// Title format: "Title - Performer1, Performer2 - Family Therapy XXX"
var titlePerformersRe = regexp.MustCompile(`^(.+)\s+-\s+(.+?)\s+-\s+Family Therapy XXX$`)
var titleSuffixRe = regexp.MustCompile(`\s+-\s+Family Therapy XXX$`)

// partMarkerRe recognises the part/episode markers the site puts where the
// cast normally goes. Reading one as a performer filed "Part 2" as a person.
var partMarkerRe = regexp.MustCompile(`(?i)^(?:part|pt\.?|episode|ep\.?|vol\.?|chapter)\s*\d+$`)

func parsePage(studioURL, pageURL string, body []byte, now time.Time) (models.Scene, bool, error) {
	meta := wputil.ParseMeta(body, "")

	var performers []string
	if m := titlePerformersRe.FindStringSubmatch(meta.Title); m != nil {
		for _, p := range strings.Split(m[2], ",") {
			p = strings.TrimSpace(p)
			// The middle segment is usually the cast, but the site also uses
			// it for a part marker ("Title - Part 2 - Family Therapy XXX"),
			// which is part of the title rather than a person.
			if p != "" && !partMarkerRe.MatchString(p) {
				performers = append(performers, p)
			}
		}
		if len(performers) > 0 {
			meta.Title = strings.TrimSpace(m[1])
		} else {
			// Nothing in that segment was a name, so it belongs to the title.
			meta.Title = titleSuffixRe.ReplaceAllString(meta.Title, "")
		}
	} else {
		meta.Title = titleSuffixRe.ReplaceAllString(meta.Title, "")
	}

	if meta.PostID == "" && len(meta.Tags) == 0 {
		return models.Scene{}, true, nil
	}

	id := meta.PostID
	if id == "" {
		id = wputil.SlugFromURL(pageURL)
	}

	tagSet := make(map[string]bool)
	var tags []string
	for _, t := range meta.Tags {
		if !tagSet[t] {
			tagSet[t] = true
			tags = append(tags, t)
		}
	}
	for _, c := range meta.Categories {
		if !tagSet[c] {
			tagSet[c] = true
			tags = append(tags, c)
		}
	}

	width := meta.Width
	height := meta.Height
	resolution := ""
	if width == 0 && height > 0 {
		width = wputil.VideoWidth(height)
	}
	if height >= 2160 {
		resolution = "4K"
	} else if height >= 1080 {
		resolution = "1080p"
	} else if height >= 720 {
		resolution = "720p"
	}

	scene := models.Scene{
		ID:          id,
		SiteID:      "familytherapy",
		StudioURL:   studioURL,
		Title:       meta.Title,
		URL:         pageURL,
		Date:        meta.Date,
		Description: meta.Description,
		Thumbnail:   meta.Thumbnail,
		Performers:  performers,
		Studio:      "Family Therapy",
		Tags:        tags,
		Width:       width,
		Height:      height,
		Resolution:  resolution,
		ScrapedAt:   now,
	}

	return scene, false, nil
}
